BINARY_NAME := jat
BUILD_DIR := ./bin
# ~/.local/bin is the XDG user-binary directory and is on PATH by default on
# Debian and macOS shells; ~/bin only joins PATH if it existed at login, which
# is exactly the trap a fresh install falls into.
INSTALL_DIR ?= $(HOME)/.local/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)

.PHONY: build test vet install clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) .

test:
	go test ./...

vet:
	go vet ./...

# Staged into the destination directory and renamed into place, never written
# over: a write-in-place keeps the destination's inode, and doing that to a
# binary another process still has mapped as running code can wedge that inode.
# A rename swaps the directory entry instead, so a running jat is untouched.
install: build
	@mkdir -p $(INSTALL_DIR)
	@staged=$(INSTALL_DIR)/$(BINARY_NAME).new-$$$$; \
	cp $(BUILD_DIR)/$(BINARY_NAME) $$staged && chmod 0755 $$staged && mv $$staged $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "installed $(INSTALL_DIR)/$(BINARY_NAME) ($(VERSION))"
	@case ":$$PATH:" in *":$(INSTALL_DIR):"*) ;; *) \
	  echo ""; \
	  echo "$(INSTALL_DIR) is not on your PATH. Add it and reload:"; \
	  echo "  echo 'export PATH=\"$(INSTALL_DIR):\$$PATH\"' >> ~/.$${SHELL##*/}rc && exec $${SHELL##*/}"; \
	  echo "or install somewhere already on it:  make install INSTALL_DIR=/usr/local/bin";; esac
	@if [ -x "$(HOME)/bin/$(BINARY_NAME)" ] && [ "$(INSTALL_DIR)" != "$(HOME)/bin" ]; then \
	  echo "note: an older copy is at $(HOME)/bin/$(BINARY_NAME); remove it so the shell cannot pick it first"; fi

clean:
	rm -f $(BINARY_NAME)
