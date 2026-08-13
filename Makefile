.PHONY: deps test test-integration fmt lint lint-warn check lint-install run run-https certs \
        migrate-up migrate-down migrate-status \
        dev-up dev-down \
        build-agent-freebsd build-agent-all build-server-all build-all release

# ── Config ─────────────────────────────────────────────────────────────────
DATABASE_URL     ?= postgres://opensourcebackup:dev_password@localhost:5432/opensourcebackup?sslmode=disable
MIGRATIONS_PATH  := migrations
CONTROL_PLANE    := ./cmd/control-plane
AGENT            := ./cmd/agent
MIGRATE_BIN      := migrate

# ── Dependencies ────────────────────────────────────────────────────────────
deps:
	go mod download

# ── Tests ───────────────────────────────────────────────────────────────────
test:
	go test ./...

test-integration:
	DATABASE_URL=$(DATABASE_URL) go test -p 1 -tags=integration ./...

# ── Format ──────────────────────────────────────────────────────────────────
fmt:
	gofmt -w .
	goimports -w -local github.com/cerberus8484/opensourcebackup .

# ── Lint ────────────────────────────────────────────────────────────────────
# Nur eigene Packages — web/node_modules enthält npm-Go-Dateien die wir nicht kontrollieren
GO_PKGS := ./cmd/... ./internal/...

# Schicht 1: blockiert — Verletzung = kein Merge (siehe .golangci.hard.yml)
lint:
	golangci-lint run --config .golangci.hard.yml $(GO_PKGS)

# Schicht 2: Baustellen — blockiert nie (siehe .golangci.warn.yml)
lint-warn:
	golangci-lint run --config .golangci.warn.yml $(GO_PKGS)

# Alles in einem: fmt → lint → test
check: fmt lint test

# golangci-lint v2 installieren (einmalig)
lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

# ── Run ─────────────────────────────────────────────────────────────────────
run:
	DATABASE_URL=$(DATABASE_URL) go run $(CONTROL_PLANE)

run-https:
	DATABASE_URL=$(DATABASE_URL) \
	LISTEN_ADDR=:8443 \
	TLS_CERT_FILE=certs/dev.crt \
	TLS_KEY_FILE=certs/dev.key \
	go run $(CONTROL_PLANE)

run-agent:
	go run $(AGENT)

# ── TLS Dev Certificate ──────────────────────────────────────────────────────
# Generates a self-signed certificate using Go stdlib — no openssl required.
# Usage:
#   make certs                        # localhost only
#   make certs TLS_IP=192.168.1.100   # + your LAN IP (for Proxmox/LAN access)
TLS_IP ?=
certs:
	go run ./internal/tools/gencert/ $(TLS_IP)

# ── Agent Release Builds ─────────────────────────────────────────────────────
VERSION ?= v0.1.0

build-agent-windows:
	GOOS=windows GOARCH=amd64 go build -o dist/agent/$(VERSION)/opensourcebackup-agent-windows-amd64.exe $(AGENT)

build-agent-linux:
	GOOS=linux GOARCH=amd64 go build -o dist/agent/$(VERSION)/opensourcebackup-agent-linux-amd64 $(AGENT)

build-agent-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o dist/agent/$(VERSION)/opensourcebackup-agent-linux-arm64 $(AGENT)

build-agent-darwin:
	GOOS=darwin GOARCH=arm64 go build -o dist/agent/$(VERSION)/opensourcebackup-agent-darwin-arm64 $(AGENT)

build-agent-freebsd:
	GOOS=freebsd GOARCH=amd64 go build -o dist/agent/$(VERSION)/opensourcebackup-agent-freebsd-amd64 $(AGENT)

build-agent-all: build-agent-windows build-agent-linux build-agent-linux-arm64 build-agent-freebsd build-agent-darwin

build-server-linux:
	GOOS=linux GOARCH=amd64 go build -o dist/server/$(VERSION)/opensourcebackup-server-linux-amd64 $(CONTROL_PLANE)

build-server-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o dist/server/$(VERSION)/opensourcebackup-server-linux-arm64 $(CONTROL_PLANE)

build-server-all: build-server-linux build-server-linux-arm64

build-all: build-agent-all build-server-all

# ── Windows Installer (MSI + EXE) ────────────────────────────────────────────
# Requires: NSIS (makensis) + WiX (dotnet tool install -g wix)
# On Linux/CI: install wine + nsis or use GitHub Actions Windows runner
installer-windows: build-agent-windows
	powershell -File scripts/build-release.ps1 -Version $(VERSION)

# ── Full release (binaries + installers + checksums) ─────────────────────────
release: build-all
	powershell -File scripts/build-release.ps1 -Version $(VERSION)

# ── Migrations ──────────────────────────────────────────────────────────────
# Requires: go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate-up:
	$(MIGRATE_BIN) -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" up

migrate-down:
	$(MIGRATE_BIN) -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" down

migrate-status:
	$(MIGRATE_BIN) -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" version

# ── Docker Dev Stack ─────────────────────────────────────────────────────────
dev-up:
	docker compose -f deployments/docker-compose/dev.yml up -d

dev-down:
	docker compose -f deployments/docker-compose/dev.yml down
