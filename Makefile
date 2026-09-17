BINARY_NAME := jat
BUILD_DIR := ./bin
INSTALL_DIR ?= $(HOME)/bin
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

clean:
	rm -f $(BINARY_NAME)
