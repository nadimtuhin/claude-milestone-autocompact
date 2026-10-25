.PHONY: build test

build:
	go build -o claude-compact-milestone ./cmd/claude-compact-milestone

test:
	go test -race ./...
