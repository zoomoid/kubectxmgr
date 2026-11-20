.DEFAULT_GOAL := build

.PHONY: build install test coverage vet lint fmt fmt-check check release-check snapshot

build:
	go build -trimpath -o switcher ./cmd/switcher

install:
	go install -trimpath ./cmd/switcher

test:
	go test -race ./...

coverage:
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

fmt-check:
	golangci-lint fmt --diff

check: fmt-check vet test lint release-check

release-check:
	# Exit 2 means valid but deprecated; brew services still requires a formula.
	goreleaser check || test "$$?" -eq 2

# Snapshot mode never publishes or requires release credentials.
snapshot: release-check
	goreleaser release --snapshot --clean
