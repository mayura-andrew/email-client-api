# Optional local overrides go in .envrc, e.g. EMAIL_API_DB_DSN=postgres://...
-include .envrc

# Project-specific names on purpose: a DB_DSN/REDIS_URL exported in the shell
# for another project must never be picked up by run/api or the migrate targets.
# Defaults match the ports docker-compose.yml exposes on the host.
EMAIL_API_DB_DSN ?= postgres://user:password@localhost:5434/emailclient?sslmode=disable
EMAIL_API_REDIS_URL ?= redis://localhost:6380/0

# Disposable containers used only by test/integration (wiped on every run).
TEST_PG_PORT ?= 55432
TEST_REDIS_PORT ?= 56379

.DEFAULT_GOAL := help

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^## \([^:]*\): /  \1\t/p' $(MAKEFILE_LIST) | column -t -s "$$(printf '\t')"

.PHONY: confirm
confirm:
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## run/deps: start PostgreSQL and Redis in Docker for local development
.PHONY: run/deps
run/deps:
	docker compose up -d --wait db redis

## run/mailpit: start the Mailpit SMTP catcher (SMTP localhost:1025, UI http://localhost:8025)
.PHONY: run/mailpit
run/mailpit:
	docker compose --profile dev up -d mailpit

## run/api: run the API locally against the Docker PostgreSQL and Redis
.PHONY: run/api
run/api:
	go run ./cmd/api -db-dsn='${EMAIL_API_DB_DSN}' -redis-url='${EMAIL_API_REDIS_URL}'

## run/web: run the admin UI dev server on Bun
.PHONY: run/web
run/web:
	cd web && bun install && bun run dev

## up: build and start the full stack (db, redis, api, web)
.PHONY: up
up:
	docker compose up -d --build

## down: stop the stack (data volumes are kept)
.PHONY: down
down:
	docker compose down

## logs: follow API logs
.PHONY: logs
logs:
	docker compose logs -f --tail=100 api

## db/psql: connect to the database using psql
.PHONY: db/psql
db/psql:
	psql '${EMAIL_API_DB_DSN}'

## db/migrations/new name=$1: create a new database migration
.PHONY: db/migrations/new
db/migrations/new:
	@test -n "${name}" || (echo 'usage: make db/migrations/new name=<name>' && exit 1)
	@echo "Creating migration files for ${name}..."
	migrate create -seq -ext=.sql -dir=./migrations ${name}

## db/migrations/up: apply all up database migrations
.PHONY: db/migrations/up
db/migrations/up: confirm
	@echo "Running up migrations..."
	migrate -path ./migrations -database '${EMAIL_API_DB_DSN}' up

## db/migrations/down: roll back the most recent migration
.PHONY: db/migrations/down
db/migrations/down: confirm
	@echo "Rolling back one migration..."
	migrate -path ./migrations -database '${EMAIL_API_DB_DSN}' down 1

## redis/cli: open redis-cli against the Docker Redis
.PHONY: redis/cli
redis/cli:
	docker compose exec redis redis-cli

## outbox/status: show outbox queue depth and stream consumer state
.PHONY: outbox/status
outbox/status:
	@psql '${EMAIL_API_DB_DSN}' -c "SELECT topic, status, COUNT(*) FROM outbox_messages GROUP BY 1, 2 ORDER BY 1, 2"
	@docker compose exec redis redis-cli XINFO GROUPS outbox:messages

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## tidy: format code and tidy module dependencies
.PHONY: tidy
tidy:
	go fmt ./...
	go mod tidy

## test: run unit tests with the race detector
.PHONY: test
test:
	go test -race -count=1 ./...

## test/integration: run outbox integration tests against throwaway PostgreSQL and Redis containers
.PHONY: test/integration
test/integration:
	@docker rm -f email-api-test-pg email-api-test-redis >/dev/null 2>&1 || true
	docker run -d --rm --name email-api-test-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=test -p ${TEST_PG_PORT}:5432 postgres:16-alpine
	docker run -d --rm --name email-api-test-redis -p ${TEST_REDIS_PORT}:6379 redis:7.4-alpine
	@until docker exec email-api-test-pg pg_isready -U postgres -d test >/dev/null 2>&1; do sleep 1; done
	@sleep 1
	TEST_DATABASE_URL='postgres://postgres:pw@localhost:${TEST_PG_PORT}/test?sslmode=disable' \
	TEST_REDIS_URL='redis://localhost:${TEST_REDIS_PORT}/0' \
	go test -race -count=1 ./internal/outbox/; \
	status=$$?; docker rm -f email-api-test-pg email-api-test-redis >/dev/null; exit $$status

## web/typecheck: type-check the admin UI
.PHONY: web/typecheck
web/typecheck:
	cd web && bun install --frozen-lockfile && bun run typecheck

## audit: verify modules, check formatting, vet, lint and test
.PHONY: audit
audit:
	go mod verify
	@test -z "$$(gofmt -l cmd internal)" || (echo 'gofmt needed:' && gofmt -l cmd internal && exit 1)
	go vet ./...
	golangci-lint run ./...
	go test -race -count=1 ./...

# ==================================================================================== #
# BUILD
# ==================================================================================== #

## build/api: build the API binary for the host and linux/amd64
.PHONY: build/api
build/api:
	@echo "Building cmd/api..."
	go build -ldflags='-s' -o=./bin/api ./cmd/api
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s' -o=./bin/linux_amd64/api ./cmd/api

## build/web: production build of the admin UI with Bun
.PHONY: build/web
build/web:
	cd web && bun install --frozen-lockfile && bun run build

## build/docker: build the API and admin Docker images
.PHONY: build/docker
build/docker:
	docker compose build api web
