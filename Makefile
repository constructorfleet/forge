.PHONY: fmt vet build test check

fmt:
	gofmt -w .
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "gofmt needs to be run on:"; \
		gofmt -l .; \
		exit 1; \
	fi

vet:
	go vet ./...

build:
	go build ./...
	go build -o /dev/null ./cmd/forge

test:
	go test ./...

check: fmt vet build test
