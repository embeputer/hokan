.PHONY: build run-server migrate test tidy

GO ?= go
BIN_DIR ?= dist

build:
	$(GO) build -o $(BIN_DIR)/hokan-server ./cmd/hokan-server
	$(GO) build -o $(BIN_DIR)/hokan ./cmd/hokan
	$(GO) build -o $(BIN_DIR)/hokan-runner ./cmd/hokan-runner

run-server:
	$(GO) run ./cmd/hokan-server

migrate:
	$(GO) run ./cmd/hokan-server migrate

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy
