BINARY := ugptsearch
PKG := ./src
BUILD_DIR := bin

GO ?= go

.PHONY: all build run test fmt vet tidy clean

all: build

build:
	mkdir -p $(BUILD_DIR)
	$(GO) -C src build -o ../$(BUILD_DIR)/$(BINARY) .

run:
	$(GO) -C src run .

test:
	$(GO) -C src test ./...

fmt:
	$(GO) -C src fmt ./...
	gofmt -s -w src

vet:
	$(GO) -C src vet ./...

tidy:
	$(GO) -C src mod tidy

clean:
	rm -rf $(BUILD_DIR)
