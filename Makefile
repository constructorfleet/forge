.PHONY: fmt fmt-check vet build install test check

fmt:
	gofmt -w .
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "gofmt needs to be run on:"; \
		gofmt -l .; \
		exit 1; \
	fi

# fmt-check is the CI-safe counterpart to fmt: it never rewrites files, it
# only reports what would change and fails if anything is unformatted.
fmt-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "gofmt needs to be run on:"; \
		gofmt -l .; \
		exit 1; \
	fi

vet:
	go vet ./...

LDFLAGS := -X main.buildCommit=$(shell git rev-parse HEAD 2>/dev/null || echo unknown) \
           -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build:
	go build ./...
	go build -ldflags "$(LDFLAGS)" -o /dev/null ./cmd/forge

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/forge

test:
	go test ./...

check: fmt vet build test
