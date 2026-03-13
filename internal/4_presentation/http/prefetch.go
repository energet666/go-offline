package httphandlers

import (
	"context"
	"sort"
)

// downloadReport tracks what was downloaded and what was skipped.
type downloadReport struct {
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
}

// prefetchModule downloads the specified module and its dependencies (if recursive=true)
// using internal server logic, manually fetching each module and reading its go.mod.
func (s *Server) prefetchModule(ctx context.Context, modPath, version string, recursive bool, seen map[string]struct{}, budget *fetchBudget, logf func(string, ...any)) (downloadReport, error) {
	type task struct {
		modPath string
		version string
	}
	// Формируем уникальный ключ для корневого модуля. Если версия не указана, используем "latest"
	rootKey := modPath + "@" + valueOr(version, "latest")
	if seen == nil {
		seen = map[string]struct{}{}
	}
	var (
		report downloadReport
		queue  = []task{{modPath: modPath, version: version}} // Очередь модулей, которые нужно обработать
		queued = map[string]struct{}{rootKey: {}}             // Множество модулей, уже добавленных в очередь, чтобы избежать дублирования
	)

	// Обрабатываем очередь модулей и их зависимостей (BFS - поиск в ширину)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		logf("resolve %s@%s", cur.modPath, valueOr(cur.version, "latest"))

		// Шаг 1: Получаем конкретную (resolved) версию модуля, если передано "latest" или псевдоверсия
		resolvedVersion, err := s.resolveVersion(ctx, cur.modPath, cur.version)
		if err != nil {
			return report, err // Если не удалось разрешить версию, прерываем процесс
		}

		key := cur.modPath + "@" + resolvedVersion

		// Если мы уже обрабатывали эту конкретную версию модуля, пропускаем
		if _, ok := seen[key]; ok {
			logf("skip already processed %s", key)
			continue
		}
		seen[key] = struct{}{} // Отмечаем модуль как обработанный

		// Проверяем лимиты по количеству скачанных модулей
		if err := budget.noteModule(); err != nil {
			return report, err
		}

		// Шаг 2: Скачиваем сам модуль (архив с кодом и .info/.mod файлы) в кэш
		downloaded, modContent, downloadedBytes, err := s.downloadModule(ctx, cur.modPath, resolvedVersion, logf)
		if err != nil {
			return report, err
		}

		// Записываем статистику по скачанному модулю (скачан ли он сейчас или уже был в кэше)
		if downloaded {
			report.Downloaded = append(report.Downloaded, key)
			// Учитываем объем скачанных данных в лимитах (budget)
			if err := budget.noteBytes(downloadedBytes); err != nil {
				return report, err
			}
			logf("downloaded %s size=%s total=%s", humanBytes(downloadedBytes), humanBytes(budget.bytes))
		} else {
			report.Skipped = append(report.Skipped, key)
			logf("cached %s", key) // Модуль уже был загружен ранее и найден в кэше
		}

		// Если рекурсивная загрузка (загрузка зависимостей) не требуется, идем к следующему модулю
		if !recursive {
			continue
		}

		// Шаг 3: Парсим содержимое скачанного файла go.mod, чтобы найти зависимости модуля (require директивы)
		reqs, err := parseGoModRequires(string(modContent))
		if err != nil {
			continue // Если go.mod не удалось распарсить, просто пропускаем поиск зависимостей для этого модуля
		}

		// Добавляем найденные зависимости в очередь
		for _, reqMod := range reqs {
			nextKey := reqMod.Path + "@" + reqMod.Version

			// Пропускаем зависимости, которые мы уже обработали или которые уже стоят в очереди
			if _, ok := seen[nextKey]; ok {
				continue
			}
			if _, ok := queued[nextKey]; ok {
				continue
			}

			queued[nextKey] = struct{}{} // Помечаем, что модуль попал в очередь
			queue = append(queue, task{modPath: reqMod.Path, version: reqMod.Version})
			logf("queue dep %s", nextKey)
		}
	}

	// Сортируем списки для удобного и предсказуемого отображения отчета
	sort.Strings(report.Downloaded)
	sort.Strings(report.Skipped)
	return report, nil
}
