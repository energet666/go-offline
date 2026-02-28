.PHONY: all build build-frontend build-backend run clean

# Название выходного бинарного файла и папка для него
APP_NAME = go-offline
BIN_DIR = bin

all: build

# 1. Загрузка зависимостей и сборка фронтенда (Svelte + Tailwind)
build-frontend:
	@echo "=> Сборка фронтенда..."
	cd frontend && npm install && npm run build

# 2. Сборка бэкенда (Go)
build-backend:
	@echo "=> Сборка бэкенда..."
	go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/go-offline

# Полный билд (сначала фронтенд, так как он встраивается в бэкенд через embed)
build: build-frontend build-backend
	@echo "=> Сборка успешно завершена! Бинарный файл находится в ./$(BIN_DIR)/$(APP_NAME)"

# Сборка и сразу запуск
run: build
	@echo "=> Запуск приложения..."
	./$(BIN_DIR)/$(APP_NAME)

# Очистка артефактов
clean:
	@echo "=> Очистка..."
	rm -rf $(BIN_DIR)
	rm -rf frontend/node_modules
	rm -rf cmd/go-offline/web/assets
	rm -f cmd/go-offline/web/index.html
	rm -f cmd/go-offline/web/vite.svg
