BINARY   := om-node-monitor
VERSION  := 1.0.0
DIST_DIR := dist

.PHONY: build run tidy clean \
        pkg-linux-amd64 pkg-linux-arm64 \
        pkg-darwin-amd64 pkg-darwin-arm64 \
        pkg-windows-amd64 pkg-all

# ── local build ──────────────────────────────────────────────────────────────

build:
	go build -ldflags="-s -w" -o $(BINARY) .

run: build
	./$(BINARY)

tidy:
	go mod tidy

# ── cross-compile helpers ────────────────────────────────────────────────────

_build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="-s -w" \
		-o $(DIST_DIR)/$(BINARY)-$(GOOS)-$(GOARCH)/$(BINARY)$(EXT) .

_package: _build
	cp plugin.json start.sh start.cmd $(DIST_DIR)/$(BINARY)-$(GOOS)-$(GOARCH)/
	cd $(DIST_DIR) && zip -r ../$(BINARY)-$(GOOS)-$(GOARCH)-$(VERSION).zip \
		$(BINARY)-$(GOOS)-$(GOARCH)/
	@echo "SHA256: $$(sha256sum $(BINARY)-$(GOOS)-$(GOARCH)-$(VERSION).zip 2>/dev/null || \
		shasum -a 256 $(BINARY)-$(GOOS)-$(GOARCH)-$(VERSION).zip)"

pkg-linux-amd64:
	mkdir -p $(DIST_DIR)/$(BINARY)-linux-amd64
	$(MAKE) _package GOOS=linux GOARCH=amd64 EXT=

pkg-linux-arm64:
	mkdir -p $(DIST_DIR)/$(BINARY)-linux-arm64
	$(MAKE) _package GOOS=linux GOARCH=arm64 EXT=

pkg-darwin-amd64:
	mkdir -p $(DIST_DIR)/$(BINARY)-darwin-amd64
	$(MAKE) _package GOOS=darwin GOARCH=amd64 EXT=

pkg-darwin-arm64:
	mkdir -p $(DIST_DIR)/$(BINARY)-darwin-arm64
	$(MAKE) _package GOOS=darwin GOARCH=arm64 EXT=

pkg-windows-amd64:
	mkdir -p $(DIST_DIR)/$(BINARY)-windows-amd64
	$(MAKE) _package GOOS=windows GOARCH=amd64 EXT=.exe

pkg-all: pkg-linux-amd64 pkg-linux-arm64 pkg-darwin-amd64 pkg-darwin-arm64 pkg-windows-amd64

# ── clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf $(DIST_DIR)
	rm -f *.zip
