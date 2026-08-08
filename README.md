# go-offline

Локальный сервер для Go-модулей (GOPROXY) с веб-интерфейсом и prefetch зависимостей для офлайн-среды.
Prefetch выполняется через стандартный `go` CLI и локальный `GOMODCACHE`.

## Что умеет

- Отдаёт модули в формате GOPROXY из локального кэша (`GOMODCACHE/cache/download`).
- Prefetch `module@version` (включая `latest`).
- Для `go.mod`: прямые зависимости или рекурсивный `go mod download`.
- Веб-интерфейс на **Svelte 5** для управления загрузками и просмотра кэша.
- Поиск по кэшированным модулям (UI и API).
- **Экспорт кэша**: полный или инкрементальный (только новые пакеты).
- **Закрепление**: отслеживание запрошенных пользователем пакетов (PIN).
- **Проверка обновлений**: показывает, для каких закреплённых пакетов в апстриме появились версии новее (включая новые мажоры).

## Быстрый старт

Для сборки требуются **Go** и **Node.js** (для сборки UI на Svelte).

```bash
# 1. Загрузка зависимостей и сборка UI + Go бэкенда
make build

# 2. Запуск приложения
./bin/go-offline -listen :8080 -cache ./cache
```

Откройте UI: `http://127.0.0.1:8080`

Для Go-клиента:

```bash
go env -w GOPROXY=http://127.0.0.1:8080 GOSUMDB=off
```

## Как использовать для работы без интернета

1. На машине с интернетом запускаете сервер и через UI/API делаете prefetch нужных модулей/проектов.
2. Экспортируете кэш:
   - Через UI: кнопка **«Всё»** (полный архив) или **«Новые»** (только то, что не экспортировалось ранее).
   - Через API: `POST /api/export-cache/prepare?incremental=true` (получение ссылки) и далее `GET /api/export-cache/download?file=...`.
3. Скачается архив с именем вида `go-offline-[full|incremental]-YYYY-MM-DD.tar.gz`.
4. Переносите архив на офлайн-машину и импортируете через кнопку **«Импорт кэша»** (или `POST /api/import-cache`).
5. На офлайн-машине запускаете сервер и указываете `GOPROXY` на него.

> **Примечание:** директория `cache/` содержит только данные модулей и `user-packages.json`. Рабочие файлы (`gocache`, `proxy`, `tmp`, `exports`) хранятся отдельно в `workdir/` и не попадают в экспорт.

## API

### Список кэша

```bash
curl http://127.0.0.1:8080/api/modules
```

Ответ содержит список модулей и количество неэкспортированных (`unexported_count`):
```json
{
  "modules": [{"Module": "...", "Version": "...", "Pinned": true, "Exported": false}],
  "unexported_count": 5
}
```

С поиском по `module`/`version`:
```bash
curl 'http://127.0.0.1:8080/api/modules?q=errors'
```

### Список закреплённых (pinned) пакетов

Пакеты закрепляются автоматически при prefetch.

```bash
# Список всех закреплённых
curl http://127.0.0.1:8080/api/pinned

# Открепить пакет
curl -X DELETE http://127.0.0.1:8080/api/pinned \
  -d '{"module":"github.com/pkg/errors","version":"v0.9.1"}'
```

### Проверка обновлений закреплённых пакетов

Спрашивает у upstream-прокси последние версии всех закреплённых модулей и сообщает, где появилось что-то новее. **Требует доступа в интернет** — на офлайн-машине работать не будет.

```bash
curl http://127.0.0.1:8080/api/pinned/updates

# Принудительно перепроверить, игнорируя закэшированный результат
curl 'http://127.0.0.1:8080/api/pinned/updates?force=1'
```

```json
{
  "checked_at": "2026-08-08T23:04:40+05:00",
  "cached": false,
  "updates": [
    {
      "module": "go.bug.st/serial",
      "version": "v1.6.4",
      "latest": "v1.8.0",
      "published_at": "2026-07-15T12:25:00Z",
      "has_update": true
    },
    {
      "module": "go.yaml.in/yaml/v3",
      "version": "v3.0.4",
      "latest": "v3.0.5",
      "next_major_module": "go.yaml.in/yaml/v4",
      "next_major_version": "v4.0.0-rc.6",
      "has_update": true
    }
  ]
}
```

Отдельно проверяются новые мажорные версии: они живут по другому пути модуля (`.../v2`, `.../v3`), и `@latest` по текущему пути про них ничего не знает. Результат проверки кэшируется на `-updates-ttl` (по умолчанию 30 минут) и сбрасывается автоматически, если список закреплённых изменился.

В UI это кнопка **«Проверить обновления»** над таблицей запрошенных пакетов: жёлтый бейдж — новая версия, розовый — новый мажор, клик по бейджу сразу запускает prefetch этой версии.

### Prefetch одного модуля

```bash
curl -X POST http://127.0.0.1:8080/api/prefetch \
  -H 'Content-Type: application/json' \
  -d '{"module":"github.com/pkg/errors","version":"v0.9.1","recursive":true}'
```

Для проверки статуса загрузки и получения логов:

```bash
curl http://127.0.0.1:8080/api/download-status
```

Для отмены текущей загрузки:

```bash
curl -X POST http://127.0.0.1:8080/api/download-cancel
```

### Prefetch из go.mod

```bash
curl -X POST http://127.0.0.1:8080/api/prefetch-gomod \
  -H 'Content-Type: application/json' \
  -d '{"gomod":"module demo\n\ngo 1.22\n\nrequire github.com/pkg/errors v0.9.1\n","recursive":false}'
```

### Логи прокси-запросов

Возвращает список последних запросов к локальному GOPROXY серверу. Можно передать параметр `limit` (по умолчанию 200, максимум 1000).

```bash
curl http://127.0.0.1:8080/api/proxy-requests?limit=100
```

### Экспорт кэша

Экспорт работает в два этапа: сначала архив собирается на сервере, затем скачивается его готовый файл. Если `incremental=true`, в архив попадут только файлы, которые ранее не экспортировались.

```bash
# 1. Запуск генерации (формирует архив и возвращает JSON c ссылкой на скачивание)
curl -X POST http://127.0.0.1:8080/api/export-cache/prepare

# Пример ответа:
# {"download_url": "/api/export-cache/download?file=go-offline-full-...", "filename": "..."}

# 2. Скачивание готового архива
curl -OJ http://127.0.0.1:8080/api/export-cache/download?file=go-offline-full-2024-03-12.tar.gz
```

### Импорт кэша

Загружает ранее экспортированный архив:

```bash
# Имя файла из примера; в реальности оно содержит дату и тип
curl -X POST http://127.0.0.1:8080/api/import-cache -F archive=@go-offline-full-2024-03-12.tar.gz
```

## Флаги

- `-listen` адрес HTTP-сервера (по умолчанию `:8080`)
- `-cache` путь к папке кэша — только данные для переноса (по умолчанию `./cache`)
- `-workdir` путь к рабочей папке — gocache, proxy, tmp, exports (по умолчанию `./workdir`)
- `-upstream` upstream GOPROXY для загрузок (по умолчанию `https://proxy.golang.org`)
- `-http-timeout` timeout одного запроса к upstream (по умолчанию `5m`)
- `-go-bin` путь к бинарнику `go` (по умолчанию `go`)
- `-updates-ttl` сколько переиспользовать результат проверки обновлений (по умолчанию `30m`)

Для нестабильной сети можно увеличить timeout:

```bash
./bin/go-offline -listen :8080 -cache ./cache -http-timeout 10m
```
