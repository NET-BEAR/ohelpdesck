SHELL := /bin/bash
ENV_FILE ?= .env
COMPOSE = docker compose --env-file $(ENV_FILE) -f deploy/compose.yml
GO = $(COMPOSE) run --rm go
NODE = docker run --rm -v "$(CURDIR)/web:/app" -v ohelpdesck-npm-cache:/root/.npm -w /app node:22.22.0-bookworm-slim

.PHONY: bootstrap dev stop api worker web test test-unit test-integration test-race lint fmt-check migrate-up migrate-down migrate-status generate openapi-check test-web test-delivery build verify
bootstrap:
	docker run --rm -e ENV_FILE=$(ENV_FILE) -v "$(CURDIR):/src" -w /src python:3.12.12-slim python deploy/bootstrap.py
	$(NODE) npm ci
dev:
	$(COMPOSE) build api worker web
	$(COMPOSE) up -d --wait postgres redis minio
	$(COMPOSE) run --rm minio-init
	$(COMPOSE) run --rm migrate up
	$(COMPOSE) up -d --wait api worker web
stop:
	$(COMPOSE) down
api:
	$(COMPOSE) up -d --build api
worker:
	$(COMPOSE) up -d --build worker
web:
	$(COMPOSE) up -d --build web
test: test-integration test-web openapi-check test-delivery
test-unit:
	$(GO) go test -short ./...
test-integration:
	$(GO) go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
	docker run --rm -v "$(CURDIR):/src:ro" -w /src python:3.12.12-slim python deploy/check_coverage.py
test-race:
	$(GO) go test -race -count=1 ./...
test-web:
	$(NODE) sh -ec 'npm ci && npm run typecheck && npm run lint && npm test && npm run build'
lint:
	$(GO) go vet ./...
	$(NODE) npm run lint
fmt-check:
	$(GO) sh -ec 'test -z "$$(gofmt -l cmd internal tests)"'
migrate-up:
	$(COMPOSE) run --rm migrate up
migrate-down:
	$(COMPOSE) run --rm migrate down
migrate-status:
	$(COMPOSE) run --rm migrate status
generate:
	@echo 'No generated source code in SPEC-000; api/openapi.yaml is maintained explicitly.'
openapi-check:
	docker run --rm -v "$(CURDIR):/src:ro" -w /src python:3.12.12-slim sh -ec 'pip install --quiet --disable-pip-version-check openapi-spec-validator==0.7.2 && python deploy/validate_openapi.py'
test-delivery:
	bash -n deploy/receive-deploy.sh deploy/remote-apply.sh deploy/smoke.sh
	docker run --rm -v "$(CURDIR):/src:ro" -w /src python:3.12.12-slim python deploy/test_delivery.py
build:
	$(COMPOSE) build api worker web
security:
	$(GO) go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
	$(NODE) npm audit --audit-level=moderate
verify: test lint fmt-check test-race security generate
	git diff --exit-code -- api/openapi.yaml
