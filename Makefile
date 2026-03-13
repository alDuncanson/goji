.PHONY: fmt fmt-check test vet build ci

fmt:
	gofmt -w $(shell find . -name '*.go' -type f)

fmt-check:
	@[ -z "$(shell gofmt -l $(shell find . -name '*.go' -type f))" ]

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./cmd/goji

ci: fmt-check vet test build
