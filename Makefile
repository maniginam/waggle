.PHONY: build test clean run install

VERSION := 0.1.0
BINARY := waggle
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/waggle/

test:
	go test ./... -count=1

test-verbose:
	go test ./... -v -count=1

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -f $(BINARY) coverage.out coverage.html

run: build
	./$(BINARY) start

install:
	go install $(LDFLAGS) ./cmd/waggle/
