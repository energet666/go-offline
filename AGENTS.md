# AGENTS.md

Welcome, fellow Agent! This file provides essential context for working on the `go-offline` project.

## 🚀 Project Overview

`go-offline` is a local GOPROXY server with a web interface (Svelte 5) and prefetching capabilities. Its main goal is to allow Go development in offline or air-gapped environments by downloading and caching modules on a machine with internet access and then exporting/importing them to the offline machine.

Prefetching is not a reimplementation of the module protocol: the server shells out to the real `go` CLI with `GOMODCACHE` pointed at its own cache directory, then serves that directory as a GOPROXY.

## 🏗 Architecture (DDD & Clean Architecture)

The project follows **Domain-Driven Design (DDD)** and **Clean/Hexagonal Architecture** principles. See `ddd_architecture.md` for a deep dive.

### Layers (`internal/`)

```
internal/
├── 1_domain/cache/            # Models + ports (interfaces). Standard library only.
│   ├── models.go              # Module, PinnedEntry, ModuleUpdate, UpdatesReport
│   └── repository.go          # CacheRepository, PinnedRepository, UpdateChecker
├── 3_infrastructure/          # Adapters: the file system, the network, the go CLI.
│   ├── fs_cache/              # CacheRepository + PinnedRepository over JSON/tar.gz on disk
│   ├── goproxy/               # UpdateChecker: asks the upstream proxy for @latest
│   └── gotool/                # Downloader: wraps the 'go' CLI (mod download / list -m / mod graph)
└── 4_presentation/http/       # HTTP handlers, GOPROXY file serving, embedded UI
```

There is **no `2_application` layer**: the use cases are thin enough that they live directly in the HTTP handlers (`startDownload` in `handlers.go` is the orchestration point). If a use case ever grows a second caller — a CLI, a scheduler — that is the moment to extract it, not before.

`Server` (`4_presentation/http/server.go`) also owns the single download slot: `downloadState` holds the status, logs and cancel func of the one background job the server runs at a time. `/api/prefetch*` returns `409` while a job is running.

### ⚠️ Dependency Rule

**Inner layers NEVER depend on outer layers.**
`1_domain` <- `3_infrastructure` / `4_presentation`

`1_domain` imports nothing but the standard library. The one deliberate exception to depending on ports only: `Server` holds a concrete `*gotool.Downloader` rather than an interface, because the download flow is defined by what the `go` CLI can do.

## 🛠 Tech Stack

-   **Backend**: Go 1.25, standard library plus a single dependency — `golang.org/x/mod` (semver/module-path handling), vendored in `vendor/`.
-   **Frontend**: Svelte 5 (Runes), Tailwind CSS, daisyUI, Vite. Located in `/frontend`.
-   **Build System**: `Makefile`.

## 📂 Key Files & Directories

-   `cmd/go-offline/main.go`: Entry point — flags, directory setup, wiring (DI).
-   `cache/`: Persistent data (`gomodcache/`, `user-packages.json`, `.export-state.json`). This is what gets exported.
-   `workdir/`: Ephemeral data (`gocache/`, `tmp/`, `exports/`, and a legacy `proxy/`). `exports/` is wiped on startup. `proxy/` is only the fallback `ProxyBaseDir()` returns when `cache/gomodcache/cache/download` does not exist yet.
-   `internal/4_presentation/http/web/`: Vite build output, embedded via `go:embed` (`ui.go`). Not in git.
-   `Makefile`: `make build`, `make run`, `make test`, `make clean`.

## 💡 Guidelines for Agents

-   **Adding New Features**:
    1.  Define models and ports in `1_domain/cache`.
    2.  Implement the adapter in `3_infrastructure` (a new package per external concern).
    3.  Expose it over HTTP in `4_presentation/http` — one file per feature area (`cache.go`, `pinned_handler.go`, `updates_handler.go`, …), registered in `server.go:RegisterRoutes`.
    4.  Wire it in `cmd/go-offline/main.go`.
-   **Background work**: goes through `Server.startDownload`. It is deliberately a single slot — do not add a second concurrent job path without deciding what the UI's status panel should show.
-   **Logging inside a job**: use the `logf` passed into the work func. Never call `logf` while holding `dlState.mu` — Go mutexes are not reentrant.
-   **Error Handling**: business errors belong in the domain (`cache.ErrNoNewFiles`); handlers translate them to status codes.
-   **Frontend**: Svelte 5 Runes (`$state`, `$derived`, `$props`). Components in `frontend/src/lib/components/`, shared state in `stores.ts`, fetch helpers in `utils.ts`.
-   **User-facing strings**: the UI and job logs are in Russian; code comments and identifiers are in English.

## 🔄 Development Workflow

1.  **Backend Changes**: Modify files in `internal/`, run `make build` (or `go build ./...` when the UI has been built at least once — `go:embed web` fails without it).
2.  **Frontend Changes**: `npm run dev --prefix frontend` gives hot reload, but Vite serves no API proxy, so API calls 404 there. For anything touching data, run `make build && ./bin/go-offline` and use port 8080. See `frontend/README.md`.
3.  **Tests**: `make test` (`go test ./...`). Pure unit tests over helper functions — nothing shells out to the `go` CLI or touches the network. Covered today: `gotool.isFilesystemPath`, `goproxy.splitMajor`, the download-log ring buffer in `4_presentation/http`.

## 📁 Export/Import Logic

-   **Full Export**: archives the entire `cache/` directory as `.tar.gz`.
-   **Incremental Export**: uses `cache/.export-state.json` to track already exported files and only includes new ones. `user-packages.json` is always included; `ErrNoNewFiles` is returned when there is nothing new.
-   **Two-step download**: `POST /api/export-cache/prepare` builds the archive in `workdir/exports/` and returns a link; `GET /api/export-cache/download?file=...` streams it. This split exists so that a failure surfaces as JSON instead of a truncated download.
-   **Import**: unpacks into `cache/`, then reloads pins and merges the ones that existed locally back in, so importing an archive never drops local pins.

---
*Happy coding! If you're unsure, refer to `ddd_architecture.md` for architectural guidance.*
