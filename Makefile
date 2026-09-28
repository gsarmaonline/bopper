BIN := bin/bop

.PHONY: all build test fmt vet lint clean install spikes

all: fmt vet test build

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/bop

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# Phase 0 spikes. a3 doubles as a regression test for the Docker DNS ordering
# the overlay design depends on; run it after any Docker upgrade.
spikes:
	./spikes/a3-name-order.sh

install: build
	install -m 0755 $(BIN) $(or $(PREFIX),/usr/local)/bin/bop

clean:
	rm -rf bin
