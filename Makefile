# Leading "v" is stripped to match goreleaser's {{.Version}}, so `graft --version`
# prints the same string for local builds and released binaries.
VERSION ?= $(shell (git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD) | sed 's/^v//')
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

.PHONY: cover
cover:
	go test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: cover-html
cover-html: cover
	go tool cover -html=coverage.out -o coverage.html

.PHONY: check-tidy
check-tidy:
	go mod tidy -diff

.PHONY: check
check: check-tidy lint

.PHONY: release
release:
	goreleaser release --clean

.PHONY: release-snapshot
release-snapshot:
	goreleaser release --snapshot --clean
