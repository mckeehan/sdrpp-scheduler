BINARY   := sdrpp-scheduler
CONFIG   := config.yaml
GO       := go
GOFLAGS  :=

.PHONY: all build clean run dry-run test vet fmt tidy install

all: build

## build: Compile the binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) .

## run: Build and run with default config
run: build
	./$(BINARY) -config $(CONFIG) -verbose

## dry-run: Preview the schedule without connecting to SDR++
dry-run: build
	./$(BINARY) -config $(CONFIG) -dry-run

## vet: Run go vet
vet:
	$(GO) vet ./...

## fmt: Format source code
fmt:
	$(GO) fmt ./...

## tidy: Tidy go modules
tidy:
	$(GO) mod tidy

## test: Run unit tests (if any)
test:
	$(GO) test ./...

## clean: Remove build artifacts
clean:
	rm -f $(BINARY)

## install: Install binary to /usr/local/bin
install: build
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)

## help: Show this help
help:
	@grep -E '^##' Makefile | sed 's/## /  /'
