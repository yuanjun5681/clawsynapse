VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST_DIR := dist

.PHONY: test test-unit run build-cli install-cli dist clean

test: test-unit

test-unit:
	go test ./...

run:
	go run ./cmd/clawsynapsed --node-id node-alpha

build-cli:
	go build -ldflags "$(LDFLAGS)" -o clawsynapse ./cmd/clawsynapse

install-cli: build-cli
	@mkdir -p $(HOME)/.clawsynapse/bin
	install -m 755 clawsynapse $(HOME)/.clawsynapse/bin/clawsynapse
	@echo "installed: $(HOME)/.clawsynapse/bin/clawsynapse"
	@echo "确保 PATH 包含: export PATH=\"$(HOME)/.clawsynapse/bin:\$$PATH\""

dist:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-darwin-arm64 ./cmd/clawsynapse
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-darwin-amd64 ./cmd/clawsynapse
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-linux-amd64  ./cmd/clawsynapse
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/clawsynapse-linux-arm64  ./cmd/clawsynapse
	@echo "binaries in $(DIST_DIR)/"

clean:
	rm -rf $(DIST_DIR) clawsynapse
