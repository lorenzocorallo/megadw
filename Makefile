SHELL := /bin/sh

GO ?= go
VP ?= vp
GOVULNCHECK ?= govulncheck
DOCKER ?= docker
WEB_DIR := web
EMBED_DIR := internal/webui/dist
OUTPUT_DIR := dist
OUTPUT_BINARY := $(OUTPUT_DIR)/megadw
BENCH_BINARY := $(OUTPUT_DIR)/megadw-benchmark
FIXTURE_BINARY := $(OUTPUT_DIR)/megadw-bench-fixture

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS ?= -X github.com/lorenzocorallo/megadw/internal/buildinfo.Version=$(VERSION) -X github.com/lorenzocorallo/megadw/internal/buildinfo.Commit=$(COMMIT) -X github.com/lorenzocorallo/megadw/internal/buildinfo.BuildTime=$(BUILD_TIME)

.PHONY: dev check test test-live build clean web-install web-check web-test web-e2e web-build backend-test audit security graceful-shutdown resource-benchmark production-smoke docker-build docker-smoke

dev:
	cd $(WEB_DIR) && $(VP) dev

web-install:
	cd $(WEB_DIR) && $(VP) install --frozen-lockfile

web-check: web-install
	cd $(WEB_DIR) && $(VP) check

web-test: web-install
	cd $(WEB_DIR) && $(VP) test

web-e2e: web-install
	cd $(WEB_DIR) && $(VP) exec playwright test

web-build: web-install
	cd $(WEB_DIR) && $(VP) build

backend-test:
	$(GO) test ./...

test-live:
	MEGADW_LIVE_MEGA_URL="$(MEGADW_LIVE_MEGA_URL)" $(GO) test ./tests/integration -run TestLivePublicMegaCompatibility -count=1

check: web-check
	$(GO) fmt ./...
	$(GO) vet ./...
	$(GO) test ./...

test: web-test backend-test

build: web-build
	mkdir -p $(EMBED_DIR) $(OUTPUT_DIR)
	rm -rf $(EMBED_DIR)/*
	cp -R $(WEB_DIR)/dist/. $(EMBED_DIR)/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT_BINARY) ./cmd/megadw

audit:
	sh scripts/audit-systemd.sh
	GO="$(GO)" VP="$(VP)" sh scripts/license-audit.sh

security:
	$(GOVULNCHECK) ./...

graceful-shutdown: build
	$(GO) test ./tests/integration -run 'TestPhaseH|TestGraceful' -count=1
	GO="$(GO)" sh scripts/shutdown-smoke.sh $(OUTPUT_BINARY)

resource-benchmark:
	mkdir -p $(OUTPUT_DIR)
	$(GO) build -trimpath -o $(FIXTURE_BINARY) ./cmd/megadw-bench-fixture
	$(GO) build -trimpath -o $(BENCH_BINARY) ./cmd/megadw-benchmark
	sh scripts/resource-benchmark.sh "$(BENCH_BINARY)" "$(FIXTURE_BINARY)"

production-smoke: build
	GO="$(GO)" sh scripts/production-smoke.sh $(OUTPUT_BINARY)

docker-build:
	$(DOCKER) build --tag megadw:local .

docker-smoke:
	DOCKER="$(DOCKER)" sh scripts/docker-smoke.sh

clean:
	rm -rf $(OUTPUT_DIR) $(WEB_DIR)/dist
