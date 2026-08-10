SHELL := /bin/sh

GO ?= go
VP ?= vp
WEB_DIR := web
EMBED_DIR := internal/webui/dist
OUTPUT_DIR := dist
OUTPUT_BINARY := $(OUTPUT_DIR)/megad

.PHONY: dev check test test-live build clean web-install web-check web-test web-e2e web-build backend-test

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
	$(GO) build -trimpath -o $(OUTPUT_BINARY) ./cmd/megad

clean:
	rm -rf $(OUTPUT_DIR) $(WEB_DIR)/dist
