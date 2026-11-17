.PHONY: build test lint
build:
	go build -o switcher ./cmd/switcher
test:
	go test -race ./...
lint:
	golangci-lint run
