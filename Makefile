.PHONY: build test

build:
	go build -o claude-milestone-autocompact ./cmd/claude-milestone-autocompact

test:
	go test -race ./...
