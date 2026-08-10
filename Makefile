.PHONY: help dev dev-stop dev-logs run run-remote build test fmt vet check minio clean

help:
	@echo "Lapis Archive File Service"
	@echo ""
	@echo "First-time setup:"
	@echo "  cp .env.local.example .env.local   # local MinIO defaults, works as-is"
	@echo "  cp .env.example .env               # remote storage, fill in credentials"
	@echo ""
	@echo "Development:"
	@echo "  make dev          Start MinIO (local S3) in Docker"
	@echo "  make run          Run the service against MinIO (.env.local)"
	@echo "  make run-remote   Run the service against remote storage (.env)"
	@echo "  make dev-stop     Stop MinIO"
	@echo "  make dev-logs     Tail MinIO logs"
	@echo "  make minio        Open the MinIO console"
	@echo ""
	@echo "Quality:"
	@echo "  make check        fmt + vet + build"
	@echo "  make test         Run tests"
	@echo ""
	@echo "Production:"
	@echo "  make prod         Build and run the production container"
	@echo "  make prod-stop    Stop the production container"

dev:
	docker compose -f docker-compose.dev.yml up -d
	@echo "MinIO S3 API:  http://localhost:47470"
	@echo "MinIO console: http://localhost:47471 (minioadmin/minioadmin)"
	@echo "Postgres:      postgres://postgres:postgres@localhost:47432/lapis"
	@echo "Next: make run"

run:
	@export $$(cat .env.local | grep -v '^#' | xargs) && go run ./cmd

run-remote:
	@export $$(cat .env | grep -v '^#' | xargs) && go run ./cmd

dev-stop:
	docker compose -f docker-compose.dev.yml down

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

build:
	CGO_ENABLED=0 go build -o bin/file-service ./cmd

test:
	go test ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

check: fmt vet build

prod:
	docker compose up --build -d

prod-logs:
	docker compose logs -f app

prod-stop:
	docker compose down

minio:
	@open http://localhost:47471 2>/dev/null || echo "Open http://localhost:47471 in your browser"

clean:
	docker compose -f docker-compose.dev.yml down -v
	docker compose down -v
	rm -rf bin
