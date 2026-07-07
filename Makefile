# Leading "v" is stripped to match goreleaser's {{.Version}}, so `graft --version`
# prints the same string for local builds and released binaries.
VERSION ?= $(shell (git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD) | sed 's/^v//')
COMMIT ?= $(shell git rev-parse HEAD)
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
NEW_FROM_REV ?= HEAD

# GOOS values that carry build-tagged source (currently just linkdir_windows.go);
# check/fix lint under each so tagged files aren't silently skipped.
LINT_GOOS ?= linux windows

.PHONY: build
build:
	mkdir -p ./bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ./bin/ ./cmd/graft

.PHONY: fix
fix:
	go mod tidy
	for goos in $(LINT_GOOS); do GOOS=$$goos golangci-lint run --new-from-rev=$(NEW_FROM_REV) --fix ./... || exit 1; done

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
check: check-tidy
	golangci-lint config verify
	for goos in $(LINT_GOOS); do GOOS=$$goos golangci-lint run --new-from-rev=$(NEW_FROM_REV) ./... || exit 1; done

.PHONY: release
release:
	goreleaser release --clean

.PHONY: release-snapshot
release-snapshot:
	goreleaser release --snapshot --clean
