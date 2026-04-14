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

.PHONY: app dmg

app: build
	cp waggle desktop/src-tauri/resources/waggle
	chmod +x desktop/src-tauri/resources/waggle
	cd desktop/src-tauri && cargo tauri build

dmg: app
	@echo "DMG at desktop/src-tauri/target/release/bundle/dmg/"
	@ls desktop/src-tauri/target/release/bundle/dmg/*.dmg 2>/dev/null || echo "(no DMG found)"
