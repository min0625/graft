NEW_FROM_REV ?= HEAD

.PHONY: fix
fix:
	go mod tidy
	golangci-lint run --new-from-rev=$(NEW_FROM_REV) --fix ./...

.PHONY: lint-verify
lint-verify:
	golangci-lint config verify

.PHONY: lint
lint: lint-verify
	golangci-lint run --new-from-rev=$(NEW_FROM_REV) ./...

.PHONY: test
test:
	go test -race -failfast ./...

.PHONY: check-tidy
check-tidy:
	go mod tidy -diff

.PHONY: check
check: check-tidy lint test
