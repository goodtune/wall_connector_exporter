GO     ?= go
BINARY := wall_connector_exporter

# Strip the leading "v" of the tag so that a local build has the same version
# string as a GoReleaser build ({{.Version}} has no "v").
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
REVISION   := $(shell git rev-parse --verify -q HEAD || echo unknown)
BRANCH     := $(shell git symbolic-ref -q --short HEAD || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# The same variables that .goreleaser.yml sets.
VERSION_PKG := github.com/prometheus/common/version
LDFLAGS     := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Revision=$(REVISION) \
	-X $(VERSION_PKG).Branch=$(BRANCH) \
	-X $(VERSION_PKG).BuildUser=make \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.PHONY: all build test vet lint format format-check release-check snapshot

## all: format check, vet, tests and build
all: format-check vet test build

## build: compile the binary
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) .

## test: run all unit tests
test:
	$(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## lint: run golangci-lint (if installed)
lint:
	golangci-lint run ./...

## format: format the code
format:
	gofmt -w .

## format-check: fail if a file is not formatted
format-check:
	@test -z "$$(gofmt -l .)" || (echo "These files need gofmt:"; gofmt -l .; exit 1)

## release-check: validate .goreleaser.yml
release-check:
	goreleaser check

## snapshot: build all release artifacts into dist/ and publish nothing
snapshot:
	goreleaser release --snapshot --clean
