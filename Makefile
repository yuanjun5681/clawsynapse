VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST_DIR := dist
CHECKSUMS_FILE := $(DIST_DIR)/checksums.txt
RELEASE_NOTES_FILE ?= $(DIST_DIR)/release-notes-$(VERSION).md

.PHONY: test test-unit run build build-daemon build-cli install-cli dist checksums release-notes release-prep clean

test: test-unit

test-unit:
	go test ./...

run:
	go run ./cmd/clawsynapsed

build: build-daemon build-cli

build-daemon:
	go build -ldflags "$(LDFLAGS)" -o clawsynapsed ./cmd/clawsynapsed

build-cli:
	go build -ldflags "$(LDFLAGS)" -o clawsynapse ./cmd/clawsynapse

install-cli: build-cli
	@mkdir -p $(HOME)/.clawsynapse/bin
	install -m 755 clawsynapse $(HOME)/.clawsynapse/bin/clawsynapse
	@echo "installed: $(HOME)/.clawsynapse/bin/clawsynapse"
	@echo "确保 PATH 包含: export PATH=\"$(HOME)/.clawsynapse/bin:\$$PATH\""

dist:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-darwin-arm64 ./cmd/clawsynapsed
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-darwin-amd64 ./cmd/clawsynapsed
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-linux-amd64  ./cmd/clawsynapsed
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-linux-arm64  ./cmd/clawsynapsed
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-windows-amd64.exe ./cmd/clawsynapsed
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapsed-windows-arm64.exe ./cmd/clawsynapsed
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-darwin-arm64 ./cmd/clawsynapse
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-darwin-amd64 ./cmd/clawsynapse
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-linux-amd64  ./cmd/clawsynapse
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-linux-arm64  ./cmd/clawsynapse
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-windows-amd64.exe ./cmd/clawsynapse
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-windows-arm64.exe ./cmd/clawsynapse
	@echo "binaries in $(DIST_DIR)/"

checksums:
	@rm -f $(CHECKSUMS_FILE)
	@(cd $(DIST_DIR) && shasum -a 256 $$(find . -maxdepth 1 -type f ! -name 'checksums.txt' ! -name 'release-notes-*' -print | sed 's|^\./||' | sort) > checksums.txt)
	@echo "checksums: $(CHECKSUMS_FILE)"

release-notes:
	@mkdir -p $(DIST_DIR)
	./scripts/release-notes.sh --version "$(VERSION)" --output "$(RELEASE_NOTES_FILE)"
	@echo "release notes: $(RELEASE_NOTES_FILE)"

release-prep: test-unit dist
	$(MAKE) checksums release-notes VERSION="$(VERSION)"

clean:
	rm -rf $(DIST_DIR) clawsynapse clawsynapsed
