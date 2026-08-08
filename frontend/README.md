# frontend

Веб-интерфейс `go-offline`: Svelte 5 (Runes) + TypeScript + Vite, оформление на Tailwind CSS 4 и daisyUI.

Сборка кладётся в `../internal/4_presentation/http/web` (см. `vite.config.ts`) и вшивается в Go-бинарник через `go:embed`. Отдельного веб-сервера в проде нет — UI отдаёт сам `go-offline`.

## Сборка

Из корня репозитория:

```bash
make build
```

`make build` собирает сначала фронтенд, потом бэкенд — порядок важен: без директории `web` не компилируется `go:embed`.

Только фронтенд:

```bash
npm run build --prefix frontend
```

## Dev-режим

```bash
npm run dev --prefix frontend
```

> **Важно:** в `vite.config.ts` не настроен проксирующий `server.proxy`, а все запросы идут по относительным путям (`/api/...`). На `http://localhost:5173` они уйдут в сам Vite и вернут 404. Dev-сервер годится для правки вёрстки и стилей; всё, что ходит в API, проверяйте на собранном бинарнике (`make build && ./bin/go-offline`, порт 8080) — либо добавьте себе proxy на `http://127.0.0.1:8080`.

Проверка типов и Svelte-разметки:

```bash
npm run check --prefix frontend
```

## Структура

```
src/
├── App.svelte            # компоновка страницы + кнопки экспорта/импорта кэша
├── app.css               # Tailwind + тема daisyUI
├── main.ts               # точка входа
└── lib/
    ├── components/
    │   ├── ModulePrefetch.svelte     # форма prefetch одного module@version
    │   ├── GoModPrefetch.svelte      # prefetch из go.mod: вставка текста, выбор и drag & drop файла
    │   ├── CachedModules.svelte      # таблица кэша, поиск, пины, проверка обновлений
    │   ├── ProxyConsole.svelte       # лог запросов к локальному GOPROXY
    │   ├── ProxyInstructions.svelte  # памятка по настройке GOPROXY на клиенте
    │   └── Toast.svelte              # всплывающие уведомления
    ├── stores.ts         # общее состояние: список модулей, флаг активной загрузки, тосты
    └── utils.ts          # fetchJSON, watchDownload (поллинг статуса), копирование в буфер
```

## Что стоит знать при правках

- **Svelte 5 Runes.** `$state`, `$derived`, `$props` — не старый синтаксис `export let` / реактивных `$:`.
- **Загрузка на сервере одна.** Пока она идёт, все формы prefetch блокируются через `isDownloadingStore`; сервер на второй запрос отвечает `409`.
- **Прогресс приходит поллингом.** `watchDownload()` в `utils.ts` дёргает `/api/download-status`, пока статус не станет `done`/`error`. Никаких WebSocket/SSE.
- **Копирование в буфер.** `copyToClipboard()` падает обратно на `document.execCommand("copy")`: `navigator.clipboard` недоступен вне secure context, а UI часто открывают по IP машины в локальной сети.
- **Язык интерфейса — русский**, комментарии и имена в коде — английские.
