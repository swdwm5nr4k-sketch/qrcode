# Build, test and install the qrcode command.

BIN     := qrcode
PKG     := ./...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet fmt clean install dist

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

test:
	go test $(PKG)

vet:
	go vet $(PKG)

fmt:
	gofmt -l -w .

install:
	go install -trimpath -ldflags "$(LDFLAGS)" .

# Build one release archive for every target the Go toolchain supports.
dist:
	go run ./scripts/dist -version $(VERSION) -out dist

clean:
	rm -f $(BIN)
	rm -rf dist
