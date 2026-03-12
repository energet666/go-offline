# AGENTS.md

Welcome, fellow Agent! This file provides essential context for working on the `go-offline` project.

## 🚀 Project Overview

`go-offline` is a local GOPROXY server with a web interface (Svelte 5) and prefetching capabilities. Its main goal is to allow Go development in offline or air-gapped environments by downloading and caching modules on a machine with internet access and then exporting/importing them to the offline machine.

## 🏗 Architecture (DDD & Clean Architecture)

The project strictly follows **Domain-Driven Design (DDD)** and **Clean/Hexagonal Architecture** principles. See `ddd_architecture.md` for a deep dive.

### Core Layers (`internal/`)

1.  **`1_domain/`**: The heart of the system. Contains business logic, entities, and repository interfaces (**Ports**). No external dependencies except the Go standard library.
    - `cache/`: Module registry, versions, and pinning logic.
    - `prefetch/`: Background job states and downloader interfaces.
2.  **`2_application/`**: Orchestrates domain objects (**Use Cases**).
    - `CacheService`: High-level operations for cache management.
    - `PrefetchService`: Manages background downloads.
3.  **`3_infrastructure/`**: Implementation of domain interfaces (**Adapters**). Knows about the file system, network, and CLI.
    - `fs_cache`: JSON-based storage for pins and cache index.
    - `gotool`: Wrapper around the `go` CLI for downloading modules.
    - `inmem_jobs`: Memory-based storage for job states.
4.  **`4_presentation/http/`**: The external entry point. HTTP handlers and routes.

### ⚠️ Dependency Rule

**Inner layers NEVER depend on outer layers.**
`1_domain` <- `2_application` <- `3_infrastructure`/`4_presentation`

## 🛠 Tech Stack

-   **Backend**: Go (standard library + project's DDD structure).
-   **Frontend**: Svelte 5 (Runes), Tailwind CSS, daisyUI, Vite. Located in `/frontend`.
-   **Build System**: `Makefile`.

## 📂 Key Files & Directories

-   `cmd/go-offline/`: Main entry point (Wiring/DI).
-   `cache/`: Persistent data (gomodcache, pins). This is what gets exported.
-   `workdir/`: Temporary/ephemeral data (build cache, proxy logs).
-   `Makefile`: `make build`, `make run`, `make dev`.

## 💡 Guidelines for Agents

-   **Adding New Features**:
    1.  Define models and interfaces in `1_domain`.
    2.  Implement business logic in `2_application`.
    3.  Implement infrastructure dependencies in `3_infrastructure`.
    4.  Expose functionality via HTTP in `4_presentation/http`.
    5.  Wire everything together in `cmd/go-offline/main.go`.
-   **State Management**: Use `1_domain` entities (like `JobState`) with internal locks for thread safety.
-   **Error Handling**: Use the domain layer to define business errors.
-   **Frontend**: Svelte 5 is used. Use Runes (`$state`, `$derived`, `$props`).
-   **Naming**: Follow the DDD naming conventions established in the project.

## 🔄 Development Workflow

1.  **Backend Changes**: Modify files in `internal/`. Run `make build` to recompile.
2.  **Frontend Changes**: Located in `frontend/`. Use `npm run dev` inside `frontend/` for hot-reloading (requires the backend to be running for API).
3.  **Tests**: Look for `_test.go` files. Run `go test ./...`.

## 📁 Export/Import Logic

-   **Full Export**: Zips the entire `cache/` directory.
-   **Incremental Export**: Uses `.export-state.json` to track already exported files and only includes new ones.
-   **Import**: Unpacks the archive into the `cache/` directory, merging with existing data.

---
*Happy coding! If you're unsure, refer to `ddd_architecture.md` for architectural guidance.*
