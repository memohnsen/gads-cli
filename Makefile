.PHONY: build install test generate update-spec check

# Google Ads API major version the vendored Discovery document targets.
VERSION ?= v26
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/memohnsen/gads-cli/internal/cli.Version=$(GIT_VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/googleads ./cmd/googleads

# Installs bin/googleads into $(BINDIR) and a `gads` symlink next to it.
install: build
	install -d $(BINDIR)
	install -m 0755 bin/googleads $(BINDIR)/googleads
	ln -sf googleads $(BINDIR)/gads

test:
	go test ./...

generate:
	go run ./tools/genmanifest

# Fetch the Discovery document for $(VERSION), replacing the vendored one,
# then regenerate the embedded manifest. Review the catalog diff before
# committing (see README, "Upgrading the API version").
update-spec:
	go run ./tools/fetchspec -version $(VERSION)
	go run ./tools/genmanifest

check:
	go vet ./...
	go run ./tools/genmanifest -check
	go test ./...
