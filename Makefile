SHELL := /bin/bash
BINARY := helm-unwedge
PKG := github.com/x7ssss/helm-unwedge
CMD := ./cmd/helm-unwedge
LDFLAGS := -s -w

.PHONY: all build test clean cross-compile lint

all: test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

test:
	go test -v -count=1 ./...

cross-compile:
	chmod +x scripts/build.sh && ./scripts/build.sh

clean:
	rm -rf bin/ dist/
