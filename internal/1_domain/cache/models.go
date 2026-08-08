package cache

type Module struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Time     string `json:"time,omitempty"`
	Pinned   bool   `json:"pinned,omitempty"`
	Exported bool   `json:"exported,omitempty"`
}

type PinnedEntry struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	PinnedAt string `json:"pinned_at"`
}

// ModuleUpdate описывает результат проверки закреплённого модуля на наличие
// более новой версии в апстриме.
type ModuleUpdate struct {
	Module  string `json:"module"`
	Version string `json:"version"` // закреплённая (текущая) версия
	// Latest — последняя версия по тому же пути модуля (без смены мажора).
	Latest      string `json:"latest,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	// NextMajorModule и NextMajorVersion заполняются, если у модуля появился
	// новый мажор, живущий по другому пути (например, .../v2).
	NextMajorModule  string `json:"next_major_module,omitempty"`
	NextMajorVersion string `json:"next_major_version,omitempty"`
	HasUpdate        bool   `json:"has_update"`
	// Error непустой, если версию проверить не удалось (нет сети, 404 и т.п.).
	Error string `json:"error,omitempty"`
}

// UpdatesReport — результат проверки всех закреплённых модулей.
type UpdatesReport struct {
	CheckedAt string         `json:"checked_at"`
	Cached    bool           `json:"cached"`
	Updates   []ModuleUpdate `json:"updates"`
}
