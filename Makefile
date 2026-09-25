.PHONY: build test generate update-spec check

VERSION ?= v25

build:
	go build -o bin/googleads ./cmd/googleads

test:
	go test ./...

generate:
	go run ./tools/genmanifest

update-spec:
	go run ./tools/fetchspec -version $(VERSION)

check:
	go vet ./...
	go run ./tools/genmanifest -check
	go test ./...
