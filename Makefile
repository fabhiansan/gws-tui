PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: install build test check clean

# Install the TUI binary into ~/.local/bin (override with PREFIX= or BINDIR=).
# The upstream Google Workspace CLI is also named `gws`, so the binary must
# land in a PATH directory that resolves before it — ~/.local/bin normally
# does. `go install` is intentionally avoided: it targets ~/go/bin, which is
# usually shadowed by the upstream CLI. See scripts/install.sh for the full
# installer (PATH checks, credentials, daemon setup).
install:
	mkdir -p $(BINDIR)
	go build -o $(BINDIR)/gws ./cmd/gws
	@echo "installed gws -> $(BINDIR)/gws"

build:
	go build -o ./bin/gws ./cmd/gws

test:
	go test ./...

check:
	go vet ./...
	go test ./...

clean:
	rm -rf ./bin
