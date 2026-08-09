// Package selfupdate описывает обновление самого приложения из сборок,
// опубликованных CI. Как и остальной домен, зависит только от stdlib.
package selfupdate

import (
	"context"
	"errors"
)

// ErrNoAsset означает, что в релизе нет бинаря под текущую платформу.
var ErrNoAsset = errors.New("для этой платформы сборки нет")

// ErrUpToDate означает, что опубликованная сборка совпадает с текущей.
var ErrUpToDate = errors.New("установлена последняя версия")

// Build идентифицирует сборку приложения. Version — это commit sha, который CI
// вшивает в бинарь: тег релиза скользящий (nightly) и для сравнения не годится.
type Build struct {
	Version string `json:"version"`
	BuiltAt string `json:"built_at,omitempty"`
}

// IsDev сообщает, что бинарь собран локально, а не в CI.
func (b Build) IsDev() bool { return b.Version == "" || b.Version == "dev" }

// Release — опубликованная сборка вместе со ссылкой на бинарь под конкретную
// платформу. AssetURL пуст, если подходящего бинаря в релизе нет.
type Release struct {
	Build
	AssetURL string
	SHA256   string
}

// Status — результат проверки обновлений, как его видит UI.
type Status struct {
	Current   Build  `json:"current"`
	Latest    *Build `json:"latest,omitempty"`
	HasUpdate bool   `json:"has_update"`
	// CanUpdate=false означает, что кнопку «Обновить» показывать нельзя;
	// причина — в Reason.
	CanUpdate bool   `json:"can_update"`
	Reason    string `json:"reason,omitempty"`
}

// ReleaseSource — источник опубликованных сборок.
type ReleaseSource interface {
	// Latest возвращает последнюю опубликованную сборку.
	Latest(ctx context.Context) (Release, error)
	// Download скачивает бинарь релиза в каталог dir, проверяет его по SHA256
	// и возвращает путь к скачанному файлу.
	Download(ctx context.Context, rel Release, dir string, logf func(string, ...any)) (string, error)
}

// Installer подменяет работающий исполняемый файл на скачанный.
type Installer interface {
	// StageDir возвращает каталог, в который нужно скачивать новый бинарь,
	// чтобы подмена свелась к переименованию в пределах одной ФС.
	StageDir() (string, error)
	// Apply заменяет текущий исполняемый файл на newBinary и перезапускает
	// приложение. Возврат без ошибки означает, что перезапуск запланирован.
	Apply(ctx context.Context, newBinary string, logf func(string, ...any)) error
}
