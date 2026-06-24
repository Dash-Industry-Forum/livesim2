.PHONY: all
all: test check coverage build

# templ version used to generate *_templ.go from *.templ. Keep in sync with the
# github.com/a-h/templ require in go.mod.
TEMPL_VERSION := v0.3.1020

.PHONY: build
build: templ livesim2 dashfetcher cmaf-ingest-receiver

.PHONY: templ
templ:
	go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION) generate

# templ-check regenerates the templ code and fails if it differs from what is
# committed. Use in CI/pre-commit so generated *_templ.go never goes stale.
.PHONY: templ-check
templ-check:
	go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION) generate
	git diff --exit-code -- '*_templ.go'

.PHONY: prepare
prepare:
	go mod tidy

livesim2 dashfetcher cmaf-ingest-receiver:
	go build -ldflags "-X github.com/Dash-Industry-Forum/livesim2/internal.commitVersion=$$(git describe --tags HEAD) -X github.com/Dash-Industry-Forum/livesim2/internal.commitDate=$$(git log -1 --format=%ct)" -o out/$@ ./cmd/$@/main.go

forlinux: prepare
	GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/Dash-Industry-Forum/livesim2/internal.commitVersion=$$(git describe --tags HEAD) -X github.com/Dash-Industry-Forum/livesim2/internal.commitDate=$$(git log -1 --format=%ct)" -o out-linux/livesim2 ./cmd/livesim2/main.go
	GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/Dash-Industry-Forum/livesim2/internal.commitVersion=$$(git describe --tags HEAD) -X github.com/Dash-Industry-Forum/livesim2/internal.commitDate=$$(git log -1 --format=%ct)" -o out-linux/dashfetcher ./cmd/dashfetcher/main.go

.PHONY: test
test: prepare templ
	go test ./...

.PHONY: coverage
coverage:
	# Ignore (allow) packages without any tests
	set -o pipefail
	go test ./... -coverprofile coverage.out
	set +o pipefail
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func coverage.out -o coverage.txt
	tail -1 coverage.txt

.PHONY: check
check: prepare templ
	golangci-lint run

.PHONY: clean
clean:
	rm -f out/*
	rm -r examples-out/*

.PHONY: install
install: all
	cp out/* $(GOPATH)/bin/

.PHONY: venv
	python3 -m venv venv
	venv/bin/pip install --upgrade pip
	venv/bin/pip install pre-commit==4.2.0

.PHONY: pre-commit
pre-commit: venv
	source venv/bin/activate && venv/bin/pre-commit run --all-files

.PHONY: update
update:
	go get -t -u ./...

.PHONY: check-licenses
check-licenses: prepare
	wwhrd check
