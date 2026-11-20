APP_NAME := chrometop
CMD_PATH := ./cmd/chrometop
ADDR ?= 127.0.0.1:9333
TOKEN ?=
CDP_URL ?=

.PHONY: all tidy build test run run-cdp clean vet fmt

all: tidy fmt vet test build

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

test:
	go test -v ./...

build: tidy
	go build -trimpath -ldflags="-s -w" -o $(APP_NAME) $(CMD_PATH)

run: build
	@if [ -n "$(TOKEN)" ]; then \
		./$(APP_NAME) --addr "$(ADDR)" --token "$(TOKEN)"; \
	else \
		./$(APP_NAME) --addr "$(ADDR)"; \
	fi

run-cdp: build
	./$(APP_NAME) --cdp "$(if $(CDP_URL),$(CDP_URL),http://127.0.0.1:9222)"

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe
	rm -rf dist/
