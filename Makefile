.PHONY: all build build-frontend build-backend run clean tidy test help

# Binary name and output directory
APP_NAME = go-offline
BIN_DIR = bin
FRONTEND_DIR = frontend
# Path where Vite puts the build (according to vite.config.ts)
WEB_DIST_DIR = internal/4_presentation/http/web

all: build

# 1. Build frontend
# Install is only run if node_modules doesn't exist to speed up rebuilds
build-frontend:
	@echo "=> Building frontend..."
	cd $(FRONTEND_DIR) && [ -d node_modules ] || npm install
	cd $(FRONTEND_DIR) && npm run build

# 2. Build backend (Go)
build-backend:
	@echo "=> Building backend..."
	go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/go-offline

# Full build (frontend MUST be before backend due to embed)
build: build-frontend build-backend
	@echo "=> Build successful! Binary: ./$(BIN_DIR)/$(APP_NAME)"

# Run the application
run: build
	@echo "=> Starting application..."
	./$(BIN_DIR)/$(APP_NAME)

# Run Go tests
test:
	@echo "=> Running tests..."
	go test ./...

# Update Go dependencies
tidy:
	@echo "=> Tidying Go modules..."
	go mod tidy

# Clean build artifacts
clean:
	@echo "=> Cleaning..."
	rm -rf $(BIN_DIR)
	rm -rf $(WEB_DIST_DIR)/*
	@echo "Done. node_modules preserved (use 'make clean-all' for full cleanup)."

# Full cleanup including frontend dependencies
clean-all: clean
	@echo "=> Removing frontend dependencies..."
	rm -rf $(FRONTEND_DIR)/node_modules

help:
	@echo "Available commands:"
	@echo "  make build          - Full build (front + back)"
	@echo "  make run            - Build and run"
	@echo "  make test           - Run all tests"
	@echo "  make tidy           - Run go mod tidy"
	@echo "  make clean          - Remove binaries and frontend build"
	@echo "  make clean-all      - Remove everything including node_modules"
