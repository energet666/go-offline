package cache

import (
	"errors"
	"io"
)

var ErrNoNewFiles = errors.New("no new files to export")

// CacheRepository abstracts file system operations for the module cache
type CacheRepository interface {
	// ListCached возвращает список закешированных модулей, отфильтрованный по запросу query,
	// и общее количество неэкспортированных модулей (без учета фильтра)
	ListCached(query string) ([]Module, int, error)
	// Export экспортирует закешированные модули в архив и записывает его в w (если incremental=true, экспортируются только новые)
	Export(w io.Writer, incremental bool) error
	// Import импортирует модули из архива r в кэш и возвращает количество импортированных модулей
	Import(r io.Reader) (int, error)
	// ProxyBaseDir возвращает путь к директории с закешированными файлами модулей для проксирования
	ProxyBaseDir() string
}

// PinnedRepository abstracts operations for user-pinned modules
type PinnedRepository interface {
	// Pin закрепляет указанный модуль и его версию в списке
	Pin(module, version string) error
	// Unpin удаляет указанный модуль и версию из списка закрепленных
	Unpin(module, version string) error
	// IsPinned проверяет, закреплен ли указанный модуль и версия
	IsPinned(module, version string) bool
	// ResolvePinnedLatest обновляет (разрешает) версию 'latest' до конкретной загруженной версии (resolvedVersion)
	ResolvePinnedLatest(module, resolvedVersion string)
	// List возвращает список всех закрепленных модулей
	List() []PinnedEntry
	// Reload перечитывает данные закреплённых пакетов с диска (например, после импорта архива)
	Reload() error
}
