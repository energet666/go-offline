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
