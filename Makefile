APP_NAME := chrometop
CMD_PATH := ./cmd/chrometop
ADDR ?= 127.0.0.1:9333
TOKEN ?=

.PHONY: all build test run clean vet fmt

all: fmt vet test build

fmt:
	gofmt -s -w .

vet:
	go vet ./...

test:
	go test -v ./...

build:
	go build -trimpath -ldflags="-s -w" -o $(APP_NAME) $(CMD_PATH)

run: build
	@if [ -n "$(TOKEN)" ]; then \
		./$(APP_NAME) --addr "$(ADDR)" --token "$(TOKEN)"; \
	else \
		./$(APP_NAME) --addr "$(ADDR)"; \
	fi

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe
	rm -rf dist/
