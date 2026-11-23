.PHONY: build fmt test verify promtool-test

build:
	go build -trimpath -o bin/gen-alerts ./cmd/gen-alerts

fmt:
	gofmt -w $$(find . -name '*.go')

test:
	go test ./...

verify:
	test -z "$$(gofmt -l $$(find . -name '*.go'))"
	go vet ./...
	go test ./...
	go build -trimpath -o bin/gen-alerts ./cmd/gen-alerts

# Requires the `promtool` binary (part of the Prometheus release tarball) on PATH.
promtool-test:
	promtool test rules testdata/promtool/*.test.yaml
