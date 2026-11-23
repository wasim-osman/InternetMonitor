GO ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.homebrew/bin/go)
VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG := ./cmd/imonitord
BUILD := CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)"

# GOOS/GOARCH[/GOARM] -> release asset name (exact names the install scripts download)
TARGETS := \
	linux/amd64:imonitord-linux-amd64 \
	linux/386:imonitord-linux-386 \
	linux/arm64:imonitord-linux-arm64 \
	linux/arm/7:imonitord-linux-arm \
	darwin/amd64:imonitord-darwin-amd64 \
	darwin/arm64:imonitord-darwin-arm64 \
	windows/amd64:imonitord-windows-amd64.exe \
	windows/arm64:imonitord-windows-arm64.exe

VET_TARGETS := linux/amd64 linux/386 linux/arm64 linux/arm darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all test vet vet-all build release checksums clean

all: test release

test:
	$(GO) vet ./...
	$(GO) test ./...

# go vet for every release GOOS/GOARCH (catches OS-specific compile errors).
vet-all:
	@set -e; for t in $(VET_TARGETS); do \
		echo "vet $$t"; \
		GOOS=$${t%/*} GOARCH=$${t#*/} GOARM=7 CGO_ENABLED=0 $(GO) vet ./...; \
	done

build: release

release:
	@rm -rf dist && mkdir -p dist
	@set -e; for t in $(TARGETS); do \
		plat=$${t%%:*}; name=$${t#*:}; \
		os=$$(echo $$plat | cut -d/ -f1); arch=$$(echo $$plat | cut -d/ -f2); arm=$$(echo $$plat | cut -d/ -f3); \
		echo "build $$name"; \
		GOOS=$$os GOARCH=$$arch GOARM=$$arm $(BUILD) -o dist/$$name $(PKG); \
	done
	@$(MAKE) --no-print-directory checksums
	@ls -l dist/

checksums:
	cd dist && { shasum -a 256 imonitord-* 2>/dev/null || sha256sum imonitord-*; } > SHA256SUMS
	@cat dist/SHA256SUMS

clean:
	rm -rf dist
