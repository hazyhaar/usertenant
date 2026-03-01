.PHONY: build test

build:
	CGO_ENABLED=0 go build ./...

test:
	go test -race -v -count=1 -timeout 120s ./...
