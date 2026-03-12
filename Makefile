.PHONY: all build build-frontend build-backend run clean tidy test help

# Название выходного бинарного файла и папка для него
APP_NAME = go-offline
BIN_DIR = bin
FRONTEND_DIR = frontend
# Путь, куда Vite складывает билд (согласно vite.config.ts)
WEB_DIST_DIR = internal/4_presentation/http/web

all: build

# 1. Сборка фронтенда
# Install запускается только если нет node_modules, чтобы ускорить повторные билды
build-frontend:
	@echo "=> Сборка фронтенда..."
	cd $(FRONTEND_DIR) && [ -d node_modules ] || npm install
	cd $(FRONTEND_DIR) && npm run build

# 2. Сборка бэкенда (Go)
build-backend:
	@echo "=> Сборка бэкенда..."
	go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/go-offline

# Полный билд (фронтенд обязательно ПЕРЕД бэкендом из-за embed)
build: build-frontend build-backend
	@echo "=> Сборка успешно завершена! Бинарный файл: ./$(BIN_DIR)/$(APP_NAME)"

# Запуск приложения
run: build
	@echo "=> Запуск приложения..."
	./$(BIN_DIR)/$(APP_NAME)

# Запуск тестов Go
test:
	@echo "=> Запуск тестов..."
	go test ./...

# Обновление зависимостей Go
tidy:
	@echo "=> Очистка Go модулей..."
	go mod tidy

# Очистка артефактов сборки
clean:
	@echo "=> Очистка..."
	rm -rf $(BIN_DIR)
	rm -rf $(WEB_DIST_DIR)/*
	@echo "Готово. node_modules не тронуты (используйте 'make clean-all' для полной очистки)."

# Полная очистка, включая зависимости фронтенда
clean-all: clean
	@echo "=> Удаление зависимостей фронтенда..."
	rm -rf $(FRONTEND_DIR)/node_modules

help:
	@echo "Доступные команды:"
	@echo "  make build          - Полная сборка (фронт + бэк)"
	@echo "  make run            - Сборка и запуск"
	@echo "  make test           - Запуск всех тестов"
	@echo "  make tidy           - go mod tidy"
	@echo "  make clean          - Удаление бинарников и билда фронтенда"
	@echo "  make clean-all      - Очистка всего, включая node_modules"
