# Lapis Archive File Service

The backend for [Lapis Archive](https://github.com/okoye-dev), a small open-source tool for moving a file from one device to another: upload a file, hand someone a link and a code, and you are done.

The service has one job. It issues presigned URLs so files move directly between the browser and an S3-compatible bucket (the service never handles the file bytes), and it manages the share links and access codes that gate a download.

```
Next.js client  ->  file service  ->  S3-compatible bucket
                    (Go, Gin, :6060)    (R2 / S3 / MinIO)

File bytes skip the service: the browser reads and writes the
bucket directly over presigned PUT and GET URLs.
```

## Status

| Area | State |
| --- | --- |
| Presigned upload and download | Working |
| Anonymous shares (link + one-time code, 72h expiry) | Working |
| Share store | Postgres (the bucket holds file bytes only) |
| Rate-limited, code-gated unlock | Working |
| JWKS auth, history, and revoke | Working (needs an OTP provider and `DATABASE_URL`) |
| Expiry purge worker and audit trail | Working |
| Resumable multipart uploads (pause and resume) | Working |
| Retention: files deleted after 3 days, 7 when signed in | Working (worker and audit trail) |
| Schema migrations | Embedded runner, applied automatically on boot |

## Features

- **Files never pass through the service.** Only presigned URLs do; the bytes go straight to the bucket.
- **No database for direct transfers.** The presigned upload and download flow needs no database. Shares live in Postgres, and every share endpoint returns 503 when `DATABASE_URL` is unset.
- **Works with any S3-compatible store.** AWS S3, Cloudflare R2, or MinIO for local development.
- **Stateless core.** No local persistence. Unlock rate limiting is in memory per instance, so it is not shared across replicas (see Roadmap).

## Quickstart (local)

Requires Go 1.25+ and Docker.

```bash
cp .env.local.example .env.local   # works as-is against local MinIO
make dev                           # starts MinIO + Postgres in Docker
make run                           # runs the service on :6060
```

```bash
curl http://localhost:6060/api/v1/health
```

`make dev` prints the local service URLs (MinIO console, Postgres). To run against a real bucket instead of MinIO, run `cp .env.example .env`, fill in the credentials, and `make run-remote`.

## API

All routes are under `/api/v1`.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/health` | Liveness check |
| `POST` | `/files/presign-upload` | Get a presigned PUT URL; the client uploads directly to the bucket |
| `GET` | `/files/:id` | Get a presigned download URL (`?download=true` forces save-as) |
| `POST` | `/uploads/multipart/init` | Start a resumable multipart upload (fixed 8 MiB parts) |
| `POST` | `/uploads/multipart/part` | Presign one part's PUT URL |
| `POST` | `/uploads/multipart/status` | List the parts the bucket already holds (resume) |
| `POST` | `/uploads/multipart/complete` | Assemble the parts into the final object |
| `POST` | `/uploads/multipart/abort` | Cancel the upload and discard its parts |
| `POST` | `/shares` | Create a share. Re-sharing the same file keeps its link and rotates the code (3 codes max) |
| `GET` | `/shares/:slug` | Public share metadata (name, size, expiry). No code required |
| `POST` | `/shares/:slug/unlock` | Exchange the access code for a presigned download URL |
| `GET` | `/shares` | List the authenticated caller's shares (requires a bearer token) |
| `DELETE` | `/shares/:slug` | Revoke one of the caller's shares (requires a bearer token) |

`:id` is the storage key, shaped `<uuid>_<original-filename>`. Errors return `{"error": "...", "code": <status>}`. Share endpoints return `503` when `DATABASE_URL` is unset. `GET /shares` and `DELETE /shares/:slug` are mounted only when `AUTH_JWKS_URL` is configured; without it they return `404`.

<details>
<summary>Example: upload and share</summary>

```bash
# 1. presign, then PUT the bytes straight to the bucket
curl -s -X POST http://localhost:6060/api/v1/files/presign-upload \
  -H 'Content-Type: application/json' \
  -d '{"name":"photo.jpg","size":123456,"content_type":"image/jpeg"}'
# returns {"upload_url":"...","storage_key":"<uuid>_photo.jpg", ...}
curl -X PUT --upload-file photo.jpg "<upload_url>" -H 'Content-Type: image/jpeg'

# 2. create a share, then unlock it with the code
curl -s -X POST http://localhost:6060/api/v1/shares \
  -d '{"storage_key":"<uuid>_photo.jpg"}'
# returns {"slug":"aB3xY9kQ2m","code":"7K2P9Q", ...}  (code shown once)
curl -s -X POST http://localhost:6060/api/v1/shares/aB3xY9kQ2m/unlock \
  -d '{"code":"7K2P9Q","download":true}'
# returns {"url":"<presigned download url>", ...}
```
</details>

## Configuration

Configured entirely through environment variables. Every value has a default; only the storage credentials are required for real use.

| Variable | Default | Notes |
| --- | --- | --- |
| `PORT` | `6060` | HTTP port |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` / `IDLE_TIMEOUT` | `30` / `30` / `120` | Seconds |
| `SHUTDOWN_TIMEOUT` | `5` | Graceful shutdown window (seconds) |
| `GIN_MODE` | `release` | `debug` for verbose logs |
| `LOG_LEVEL` | `info` | Log verbosity |
| `MAX_UPLOAD_MB` | `512` | Upload cap (signed into the presigned URL) |
| `ALLOWED_ORIGINS` | *(none)* | Comma-separated CORS allowlist; unset blocks all cross-origin, `*` allows any |
| `TRUSTED_PROXIES` | *(none)* | CIDRs to trust for client IP; unset means trust none |
| `S3_ENDPOINT` | *(empty)* | Empty means AWS S3; host without scheme for R2/MinIO |
| `S3_REGION` | `us-east-1` | R2 uses `auto` |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | *(empty)* | Required |
| `S3_USE_SSL` | `true` | `false` for local MinIO |
| `S3_BUCKET_NAME` | `oss-archive` | Must already exist |
| `S3_FORCE_PATH_STYLE` | `false` | `true` for MinIO |
| `DATABASE_URL` | *(empty)* | Any Postgres; enables all share endpoints and the purge/retention workers (unset means shares return 503) |
| `RETENTION_ANON_HOURS` / `RETENTION_OWNED_HOURS` | `72` / `168` | How long uploads are kept (anonymous / signed-in) |
| `AUTH_JWKS_URL` / `AUTH_ISSUER` | *(empty)* | JWKS endpoint and issuer for verifying bearer tokens; enables the history and revoke endpoints |
| `AUTH_AUDIENCE` | *(empty)* | Expected token audience (Supabase uses `authenticated`); unset skips the audience check |

The service does not create buckets, so create yours first (the dev compose does this for MinIO).

### Browser uploads need bucket CORS

Because uploads PUT directly to the bucket, the bucket must allow it. MinIO is permissive by default. For R2 or S3, add a CORS rule allowing `PUT` and `GET` from your frontend origin and exposing `ETag`.

### Reclaim abandoned multipart uploads

A multipart upload the client never completes or aborts leaves billable in-progress parts. Reclaim them with a bucket lifecycle rule rather than code: in the Cloudflare R2 dashboard (Bucket, then Settings, then Object lifecycle rules) add a rule to abort incomplete multipart uploads after 1 day (`AbortIncompleteMultipartUpload` on AWS S3). The retention worker only deletes finished objects, so this rule is what cleans up interrupted sessions.

## Auth and history

Anonymous quick-shares need no auth. History and revoke require a signed-in user, and the design stays vendor-neutral:

- **Database:** any Postgres via `DATABASE_URL`. Migrations run automatically on boot (or `make migrate`) and are tracked in `schema_migrations`. The `shares` table carries `owner_id` so history is listed per user.
- **Auth:** an Email-OTP provider that issues JWTs (Supabase is the first target). The backend verifies bearer tokens against the issuer's JWKS endpoint (`AUTH_JWKS_URL`), so switching providers is a config change. Ownership is enforced in the backend, so the schema stays plain Postgres with no vendor-specific row-level security.

## Security

- **Bytes never transit the service.** Uploads and downloads are presigned and go directly between the browser and the bucket.
- **No bucket-listing endpoint.** `GET /files/:id` is a capability URL: it presigns a download only for a caller who already holds the unguessable `<uuid>_<name>` key. There is no way to enumerate the bucket.
- **Access codes** are stored only as salted hashes, and unlock attempts are rate limited per slug and per IP.
- **Expired shares** are deleted by the purge worker (object and row), and each removal is recorded in `audit_log`.
- **Retention** deletes every upload 3 days after it lands (7 if the uploader was signed in), live shares included. Deletions and retried failures are recorded in `audit_log`, and runs in `job_runs`.

## Project layout

```
cmd/
  main.go          service entrypoint
  migrate/         migration CLI
internal/
  domain/          core types and rules, no internal deps (Share, User)
  config/          env-based configuration
  server/          HTTP server, routes, CORS, graceful shutdown
  handlers/        request handlers (files, shares, health)
  store/           Postgres share persistence
  auth/            JWKS bearer-token verification
  storage/         S3-compatible storage client
  storagekey/      builds and parses the <uuid>_<name> object keys
  worker/          periodic job runner, expiry purge, retention
  audit/           audit log and job-run history
  migrate/         embedded migration runner
  transport/rest/  shared response shapes
db/migrations/     SQL migrations (embedded)
```

## Development

```bash
make check   # gofmt + go vet + build
make test    # run tests
make help    # everything else
```

## Deploying

Any platform that runs a container and injects env vars. The `Dockerfile` builds a small Alpine image running as a non-root user on `6060`.

```bash
docker compose up --build -d   # reads credentials from .env
```

## Roadmap

- Email delivery of access codes
- Per-share attempt ceiling that survives restarts (shared store)

## License

[MIT](LICENSE)
