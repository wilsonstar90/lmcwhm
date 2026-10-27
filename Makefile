VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/wilsonstar90/lmcwhm/internal/config.Version=$(VERSION)
DEV_HOME := $(CURDIR)/.dev

.PHONY: all web build linux test dev-init dev clean

all: web build

web:
	cd web && npm ci && npm run build

# web/dist must exist for go:embed even when the UI has not been built.
build:
	@mkdir -p web/dist && touch web/dist/.keep
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/lmcwhm ./cmd/lmcwhm

# Release binaries for Ubuntu servers.
linux:
	@mkdir -p web/dist && touch web/dist/.keep
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o bin/lmcwhm-linux-amd64 ./cmd/lmcwhm
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o bin/lmcwhm-linux-arm64 ./cmd/lmcwhm

test:
	go vet ./...
	go test ./...

# Local development: data lives in ./.dev, panel on https://127.0.0.1:18888/console
dev-init: build
	LMCWHM_HOME=$(DEV_HOME) ./bin/lmcwhm init --username admin --password admin12345 --port 18888 --entry /console

dev: build
	LMCWHM_HOME=$(DEV_HOME) ./bin/lmcwhm serve

clean:
	rm -rf bin web/dist .dev
