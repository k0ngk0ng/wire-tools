export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod
export GOTMPDIR := $(CURDIR)/.cache/tmp
export TMPDIR := $(CURDIR)/.cache/tmp

.PHONY: prepare test build check
prepare:
	mkdir -p .cache/go-build .cache/go-mod .cache/tmp bin
build: prepare
	go build -trimpath -o bin/wirectl ./cmd/wirectl
	go build -trimpath -o bin/wirectl-talk ./cmd/wirectl-talk
test: prepare
	go test -race ./...
	python3 scripts/test-session.py
	python3 scripts/test-media.py
	python3 scripts/test-groups.py
	python3 scripts/test-direct.py
check: test
	go vet ./...
