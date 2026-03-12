package prefetch

import "context"

// Downloader abstracts the module downloading process (wrapped 'go' tool)
type Downloader interface {
	DownloadModule(ctx context.Context, module, version string, recursive bool, logf func(string, ...any)) error
	DownloadGoMod(ctx context.Context, goModContent string, recursive bool, logf func(string, ...any)) error
}
