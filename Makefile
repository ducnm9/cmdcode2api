# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=cmdcode2api
BINARY_UNIX=$(BINARY_NAME)_unix

all: test build

build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/cmdcode2api

test:
	$(GOTEST) -v ./...

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

run: build
	./$(BINARY_NAME)

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v ./cmd/cmdcode2api

# --- Fork workflow ---------------------------------------------------------

# Đồng bộ code mới từ repo gốc: upstream/master -> master -> develop
sync:
	./scripts/sync-upstream.sh

# Chỉ sync master theo upstream, không merge vào develop
sync-master:
	./scripts/sync-upstream.sh --no-merge

# Tạo branch tính năng mới từ develop: make feature name=ten-tinh-nang
feature:
	@test -n "$(name)" || { echo "Dùng: make feature name=ten-tinh-nang"; exit 1; }
	git checkout develop
	git pull origin develop
	git checkout -b feature/$(name)
	@echo "Đã tạo branch feature/$(name) từ develop."

.PHONY: all build test clean run build-linux sync sync-master feature
