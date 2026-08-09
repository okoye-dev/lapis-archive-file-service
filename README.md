# Lapis Archive — File Service

The Go backend for [Lapis Archive](https://github.com/okoye-dev), a small open source tool for getting a file from one device to another. Upload a file, hand someone a link and a code, done.

This service does one job: it accepts file uploads and hands out time-limited download links. Files live in any S3-compatible bucket (AWS S3, Cloudflare R2, MinIO). There is no database, no accounts, and no state in the service itself, which keeps it easy to run, easy to read, and easy to throw away and redeploy.

## How it fits together

```
Next.js client  ──►  this service  ──►  S3-compatible bucket
(lapis-archive-client)   Gin, :6060        (R2 / S3 / MinIO)
```

- The client uploads files here and lists what's in the bucket.
- Downloads never pass through this service: it returns a presigned URL and the bytes flow straight from the bucket to the recipient.
- Share links and access codes are currently generated and checked in the client. This service is deliberately unaware of them for now.

## API

All routes live under `/api/v1`.

| Method | Path | What it does |
| --- | --- | --- |
| GET | `/health` | Liveness check |
| GET | `/files` | List files in the bucket |
| POST | `/files` | Upload a file (multipart field: `file`) |
| GET | `/files/:id` | Get a presigned download URL (add `?download=true` to force save-as) |
| DELETE | `/files/:id` | Delete a file |

`:id` is the file's storage key, which has the shape `<uuid>_<original-filename>`.

Example:

```bash
curl -F "file=@photo.jpg" http://localhost:6060/api/v1/files
curl "http://localhost:6060/api/v1/files/<storage_key>?download=true"
```

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

- Server-side share records, so links work across devices
- Access-code verification on the download path
- File expiry (24h anonymous / 3 days with an email)

## License

[MIT](LICENSE)
