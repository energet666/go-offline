// Package selfinstall заменяет работающий исполняемый файл на скачанный и
// перезапускает приложение.
package selfinstall

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// restartDelay даёт UI время получить последний статус задачи до того, как
// процесс закроет порт и уйдёт на перезапуск.
const restartDelay = 2 * time.Second

// shutdownTimeout ограничивает ожидание завершения текущих запросов.
const shutdownTimeout = 5 * time.Second

// RestartWindow — сколько main ждёт после остановки сервера, прежде чем
// признать перезапуск несостоявшимся. Shutdown возвращает управление из
// Serve сразу, поэтому без этого ожидания процесс завершился бы раньше,
// чем установщик успел запустить новую копию.
const RestartWindow = 30 * time.Second

// Installer подменяет бинарь и перезапускает процесс.
type Installer struct {
	// shutdown освобождает слушающий сокет перед стартом новой копии:
	// иначе она не сможет занять тот же порт.
	shutdown func(context.Context) error
	// exit вынесен в поле, чтобы тесты не завершали процесс.
	exit func(int)
}

// New создаёт установщик. shutdown обычно — метод Shutdown у http.Server.
func New(shutdown func(context.Context) error) *Installer {
	return &Installer{shutdown: shutdown, exit: os.Exit}
}

// executablePath возвращает путь к текущему бинарю с раскрытыми симлинками:
// подменять надо сам файл, а не ссылку на него.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("определить путь к исполняемому файлу: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func (i *Installer) StageDir() (string, error) {
	exe, err := executablePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// backupPath — куда уезжает старый бинарь на Windows.
func backupPath(exe string) string { return exe + ".old" }

// CleanupBackup удаляет бинарь, оставшийся от предыдущего обновления.
// Вызывается на старте: пока процесс работал, удалить свой прежний файл
// Windows не давал.
func CleanupBackup() {
	exe, err := executablePath()
	if err != nil {
		return
	}
	_ = os.Remove(backupPath(exe))
}

func (i *Installer) Apply(ctx context.Context, newBinary string, logf func(string, ...any)) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}

	if err := swap(exe, newBinary); err != nil {
		return err
	}
	logf("исполняемый файл заменён: %s", exe)

	logf("перезапуск через %s", restartDelay)
	go i.restart(exe)
	return nil
}

// swap ставит новый бинарь на место exe.
func swap(exe, newBinary string) error {
	if runtime.GOOS != "windows" {
		// На POSIX переименование поверх работающего файла разрешено:
		// процесс продолжает жить со старым inode.
		if err := os.Rename(newBinary, exe); err != nil {
			return fmt.Errorf("заменить исполняемый файл: %w", err)
		}
		return nil
	}

	// Windows не даёт перезаписать запущенный .exe, но даёт его переименовать.
	backup := backupPath(exe)
	_ = os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return fmt.Errorf("отодвинуть текущий исполняемый файл: %w", err)
	}
	if err := os.Rename(newBinary, exe); err != nil {
		// Откатываемся, иначе приложение останется без бинаря.
		if rbErr := os.Rename(backup, exe); rbErr != nil {
			return fmt.Errorf("заменить исполняемый файл: %w (откат не удался: %v)", err, rbErr)
		}
		return fmt.Errorf("заменить исполняемый файл: %w", err)
	}
	return nil
}

// restart закрывает сервер и поднимает новую копию с теми же аргументами.
func (i *Installer) restart(exe string) {
	time.Sleep(restartDelay)

	if i.shutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := i.shutdown(ctx); err != nil {
			log.Printf("warn: graceful shutdown before restart: %v", err)
		}
		cancel()
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), RestartedEnv+"=1")
	if err := cmd.Start(); err != nil {
		log.Printf("error: не удалось запустить новую версию: %v", err)
		i.exit(1)
		return
	}
	log.Printf("новая версия запущена (pid %d), завершаемся", cmd.Process.Pid)
	i.exit(0)
}

// RestartedEnv помечает процесс, поднятый обновлением: по этому признаку он
// ждёт освобождения порта предыдущей копией.
const RestartedEnv = "GO_OFFLINE_RESTARTED"
