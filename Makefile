APP_NAME := chrometop
CMD_PATH := ./cmd/chrometop
ADDR ?= 127.0.0.1:9333
TOKEN ?=
CDP_URL ?=

# VERSION holds `version = "x.y.z"`; extract x.y.z.
VERSION ?= $(shell sed -n 's/^version = "\(.*\)"/\1/p' VERSION 2>/dev/null)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: all tidy fmt fmt-check vet test race cover build run run-cdp dist ext-zip clean help

all: tidy fmt vet test build

help:
	@echo "Targets: all tidy fmt fmt-check vet test race cover build run run-cdp dist ext-zip clean"

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

fmt-check:
	@out="$$(gofmt -s -l .)"; \
	if [ -n "$$out" ]; then echo "Unformatted files:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

cover:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(APP_NAME) $(CMD_PATH)

run: build
	@if [ -n "$(TOKEN)" ]; then \
		./$(APP_NAME) --addr "$(ADDR)" --token "$(TOKEN)"; \
	else \
		./$(APP_NAME) --addr "$(ADDR)"; \
	fi

run-cdp: build
	./$(APP_NAME) --cdp "$(if $(CDP_URL),$(CDP_URL),http://127.0.0.1:9222)"

# Cross-compile release binaries into dist/.
dist:
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" \
			-o dist/$(APP_NAME)_$(VERSION)_$${os}_$${arch}$$ext $(CMD_PATH) || exit 1; \
	done

ext-zip:
	@mkdir -p dist
	cd extension && zip -r ../dist/$(APP_NAME)-extension_$(VERSION).zip . -x '*.DS_Store'

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe coverage.out
	rm -rf dist/
