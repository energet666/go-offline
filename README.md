# go-offline

Локальный сервер для Go-модулей (GOPROXY) с веб-интерфейсом и prefetch зависимостей для офлайн-среды.
Prefetch выполняется через стандартный `go` CLI и локальный `GOMODCACHE`.

## Что умеет

- Отдаёт модули в формате GOPROXY из локального кэша (`GOMODCACHE/cache/download`).
- Prefetch `module@version` (включая `latest`).
- Для `go.mod`: прямые зависимости или рекурсивный `go mod download all`.
- Веб-интерфейс для управления загрузками и просмотра кэша.

## Быстрый старт

```bash
go build -o go-offline ./cmd/go-offline
./go-offline -listen :8080 -cache ./cache
```

Откройте UI: `http://127.0.0.1:8080`

Для Go-клиента:

```bash
go env -w GOPROXY=http://127.0.0.1:8080
go env -w GOSUMDB=off
```

## Как использовать для работы без интернета

1. На машине с интернетом запускаете сервер и через UI/API делаете prefetch нужных модулей/проектов.
2. Копируете папку кэша (по умолчанию `./cache`) на рабочую офлайн-машину.
3. На офлайн-машине запускаете сервер с этим кэшем и указываете `GOPROXY` на него.

## API

### Список кэша

```bash
curl http://127.0.0.1:8080/api/modules
```

### Prefetch одного модуля

```bash
curl -X POST http://127.0.0.1:8080/api/prefetch \
  -H 'Content-Type: application/json' \
  -d '{"module":"github.com/pkg/errors","version":"v0.9.1","recursive":true}'
```

Ответ вернёт `job_id`. Статус и логи:

```bash
curl http://127.0.0.1:8080/api/jobs/j-1
```

### Prefetch из go.mod

```bash
curl -X POST http://127.0.0.1:8080/api/prefetch-gomod \
  -H 'Content-Type: application/json' \
  -d '{"gomod":"module demo\n\ngo 1.22\n\nrequire github.com/pkg/errors v0.9.1\n","recursive":false}'
```

## Флаги

- `-listen` адрес HTTP-сервера (по умолчанию `:8080`)
- `-cache` путь к папке кэша (по умолчанию `./cache`)
- `-upstream` upstream GOPROXY для загрузок (по умолчанию `https://proxy.golang.org`)
- `-http-timeout` timeout одного запроса к upstream (по умолчанию `5m`)
- `-fetch-retries` число повторов при timeout/429/5xx (по умолчанию `3`)
- `-max-job-bytes` лимит объёма скачивания за 1 задачу (по умолчанию `2147483648`)
- `-max-job-modules` лимит числа модулей за 1 задачу (по умолчанию `4000`)
- `-go-bin` путь к бинарнику `go` (по умолчанию `go`)

Для нестабильной сети можно стартовать так:

```bash
./go-offline -listen :8080 -cache ./cache -http-timeout 10m -fetch-retries 6
```
