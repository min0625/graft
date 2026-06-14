VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD)
COMMIT ?= $(shell git rev-parse HEAD)
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
NEW_FROM_REV ?= HEAD

.PHONY: build
build:
	mkdir -p ./bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ./bin/ ./cmd/graft

.PHONY: fix
fix:
	go mod tidy
	golangci-lint run --new-from-rev=$(NEW_FROM_REV) --fix ./...

.PHONY: lint
lint:
	golangci-lint config verify
	golangci-lint run --new-from-rev=$(NEW_FROM_REV) ./...

.PHONY: test
test:
	go test -race -failfast ./...

.PHONY: check-tidy
check-tidy:
	go mod tidy -diff

.PHONY: check
check: check-tidy lint test

.PHONY: release
release:
	goreleaser release --clean

.PHONY: release-snapshot
release-snapshot:
	goreleaser release --snapshot --clean
