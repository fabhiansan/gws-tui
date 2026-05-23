PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: install build test check clean

# Install the TUI binary into ~/.local/bin (override with PREFIX= or BINDIR=).
# The binary is `gws-tui`; the upstream Google Workspace CLI (`gws`) is a
# separate install. See scripts/install.sh for the full installer.
install:
	mkdir -p $(BINDIR)
	go build -o $(BINDIR)/gws-tui ./cmd/gws-tui
	@echo "installed gws-tui -> $(BINDIR)/gws-tui"

build:
	go build -o ./bin/gws-tui ./cmd/gws-tui

test:
	go test ./...

check:
	go vet ./...
	go test ./...

clean:
	rm -rf ./bin
