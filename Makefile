.PHONY: build run test lint clean install

BINARY := bin/disk-analyzer
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run:
	go run . $(ARGS)

test:
	go test ./... -count=1

race:
	go test -race ./... -count=1

lint:
	golangci-lint run

clean:
	rm -rf bin/ tmp/

install: build
	install -m 0755 $(BINARY) $(HOME)/.local/bin/disk-analyzer