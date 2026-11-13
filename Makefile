.PHONY: setup ui-deps ui ui-stub ui-dev ui-check build run test lint dev-env dev-up dev-down dev-logs dev-admin e2e docker

setup: ui-deps
	go mod download

ui-deps:
	cd ui && npm ci

# Build the SvelteKit app into ui/build (embedded by the Go binary).
ui:
	cd ui && npm run build

# Placeholder so `go build` / `go vet` work without building the UI.
ui-stub:
	@mkdir -p ui/build && [ -n "$$(ls -A ui/build 2>/dev/null)" ] || touch ui/build/.stub

# Vite dev server on :5173, proxying /api to the BFF on :8090.
ui-dev:
	cd ui && npm run dev

ui-check:
	cd ui && npm run check && npm run lint

build: ui
	CGO_ENABLED=0 go build -trimpath -o bin/admin ./cmd

# The BFF on the host (:8090) against the dev compose's auth service.
run: ui-stub
	ENV=development HTTP_ADDR=:8090 INTERNAL_ADDR=:8091 PUBLIC_URL=http://localhost:8090 COOKIE_SECURE=false \
	AUTH_URL=http://localhost:8080 go run ./cmd serve

test: ui-stub
	go test -race ./...

lint: ui-stub
	go vet ./...
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))" || (gofmt -l $$(git ls-files '*.go') && exit 1)

dev-env:
	./scripts/dev-env.sh

dev-up:
	docker compose up -d --build

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f admin auth

# Creates an admin in the dev auth service; prints a generated password once.
dev-admin:
	docker compose exec auth ./auth user create --username admin --email admin@example.com --role admin

# End-to-end check against the running dev compose.
e2e:
	./scripts/e2e.sh

docker:
	docker build --build-arg VERSION=dev -t boson-admin:dev .
