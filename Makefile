VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt lint run docker e2e clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/nextcloud-mcp-fast .

test:
	go test ./...

# End-to-end: builds the binary, then drives it over stdio against an in-process
# WebDAV mock. Gated behind the `e2e` build tag so it never runs in `make test`.
e2e: build
	go test -race -tags=e2e -count=1 ./test/e2e/

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet fmt

run:
	go run .

docker:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/valdrent/nextcloud-mcp-fast:$(VERSION) .

clean:
	rm -rf bin dist
