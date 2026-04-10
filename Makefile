export GOTOOLCHAIN := go1.25.5

.PHONY: build test clean

build:
	go build -o node ./cmd/node

test:
	go test ./...

clean:
	go clean ./...
