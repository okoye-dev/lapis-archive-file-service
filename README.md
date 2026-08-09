# Lapis Archive — File Service

The Go backend for [Lapis Archive](https://github.com/okoye-dev), a small open source tool for getting a file from one device to another. Upload a file, hand someone a link and a code, done.

This service does one job: it accepts file uploads and hands out time-limited download links. Files live in any S3-compatible bucket (AWS S3, Cloudflare R2, MinIO). There is no database, no accounts, and no state in the service itself, which keeps it easy to run, easy to read, and easy to throw away and redeploy.

## How it fits together

```
Next.js client  ──►  this service  ──►  S3-compatible bucket
(lapis-archive-client)   Gin, :6060        (R2 / S3 / MinIO)
```

- File bytes never pass through this service. Uploads use a presigned PUT URL and downloads a presigned GET URL, so bytes flow directly between the client and the bucket.
- The service only handles the control plane: issuing presigned URLs, listing/deleting, and storing small share records.
- There is no database. Share records are small JSON objects kept in the bucket under `shares/<slug>.json`, with the access code stored only as a salted hash.

## API

All routes live under `/api/v1`.

| Method | Path | What it does |
| --- | --- | --- |
| GET | `/health` | Liveness check |
| GET | `/files` | List files in the bucket |
| POST | `/files/presign-upload` | Get a presigned PUT URL, then upload straight to the bucket |
| GET | `/files/:id` | Get a presigned download URL (add `?download=true` to force save-as) |
| DELETE | `/files/:id` | Delete a file |
| POST | `/shares` | Create a share for a file: returns a slug and a one-time-shown access code |
| GET | `/shares/:slug` | Public share metadata (file name, size, expiry) — no code needed |
| POST | `/shares/:slug/unlock` | Exchange the access code for a presigned download URL |

`:id` is the file's storage key, which has the shape `<uuid>_<original-filename>`.

Uploading (bytes go straight to the bucket, never through this service):

```bash
curl -s -X POST http://localhost:6060/api/v1/files/presign-upload \
  -H 'Content-Type: application/json' \
  -d '{"name":"photo.jpg","size":123456,"content_type":"image/jpeg"}'
# → {"upload_url":"...","storage_key":"<uuid>_photo.jpg",...}
curl -X PUT --upload-file photo.jpg "<upload_url>" -H 'Content-Type: image/jpeg'
```

Sharing:

```bash
curl -s -X POST http://localhost:6060/api/v1/shares \
  -d '{"storage_key":"<uuid>_photo.jpg"}'
# → {"slug":"aB3xY9kQ2m","code":"7K2P9Q",...}  code is only shown once
curl -s -X POST http://localhost:6060/api/v1/shares/aB3xY9kQ2m/unlock \
  -d '{"code":"7K2P9Q","download":true}'
# → {"url":"<presigned download url>",...}
```

Share metadata lives in the bucket under `shares/<slug>.json` with the access code stored as a salted hash — there is no database. Unlock attempts are rate limited per slug and IP.

Errors come back as `{"error": "message", "code": 500}` with a matching HTTP status.

## Running it locally

You need Go 1.24+ and Docker (for MinIO, the local stand-in for S3).

```bash
cp .env.local.example .env.local   # works as-is, no editing needed
make dev                           # starts MinIO + creates the bucket
make run                           # runs the service on :6060
```

Check it's alive:

```bash
curl http://localhost:6060/api/v1/health
```

To run against a real bucket instead of MinIO:

```bash
cp .env.example .env               # fill in your credentials
make run-remote
```

## Configuration

Everything is configured through environment variables. Every value has a default, so only the storage credentials are truly required.

| Variable | Default | Notes |
| --- | --- | --- |
| `PORT` | `6060` | HTTP port |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` / `IDLE_TIMEOUT` | `30` / `30` / `120` | Seconds |
| `SHUTDOWN_TIMEOUT` | `5` | Graceful shutdown window, seconds |
| `GIN_MODE` | `release` | `debug` for verbose request logs |
| `LOG_LEVEL` | `info` | |
| `S3_ENDPOINT` | *(empty)* | Empty means AWS S3. For R2/MinIO set the host, no scheme |
| `S3_REGION` | `us-east-1` | R2 uses `auto` |
| `S3_ACCESS_KEY_ID` | *(empty)* | Required |
| `S3_SECRET_ACCESS_KEY` | *(empty)* | Required |
| `S3_USE_SSL` | `true` | `false` for local MinIO |
| `S3_BUCKET_NAME` | `oss-archive` | Bucket must already exist |
| `S3_FORCE_PATH_STYLE` | `false` | `true` for MinIO |
| `MAX_UPLOAD_MB` | `512` | Upload size cap, enforced in the presigned signature too |
| `ALLOWED_ORIGINS` | `*` | Comma-separated CORS allowlist for browsers |

For presigned uploads from a browser, the *bucket* needs CORS too (allow `PUT` from your client origin). MinIO in the dev compose file already permits this; on R2/S3 add a CORS rule to the bucket.

The service never creates buckets. Create yours first (the dev compose file does this for MinIO automatically).

## Project layout

```
cmd/               entrypoint
internal/
  config/          env-based configuration
  server/          HTTP server, routes, CORS, graceful shutdown
  handlers/        request handlers (files, health)
  storage/         S3-compatible storage client
  transport/rest/  shared response shapes
```

## Development

```bash
make check   # gofmt + go vet + build
make test    # run tests
make help    # everything else
```

## Deploying

Any platform that runs a container and injects env vars works. The `Dockerfile` builds a small Alpine image that runs as a non-root user and listens on `6060`.

```bash
docker compose up --build -d   # uses .env for credentials
```

For Railway and friends: point it at the repo, set the `S3_*` variables, done.

## Roadmap

- Gate `GET /files` and `DELETE /files/:id` behind an admin token (they are open today)
- Client integration: presigned uploads + server-side shares end to end
- Bucket lifecycle rules for physical deletion of expired files
- Email delivery of access codes

## License

[MIT](LICENSE)
