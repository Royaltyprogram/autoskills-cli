package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const collectWatchDebounce = 500 * time.Millisecond

func watchCollectSessionChanges(ctx context.Context, sessionFile, codexHome string) (<-chan struct{}, <-chan error, func() error, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, nil, err
	}

	watchedDirs := make(map[string]struct{}, 16)
	targetFile := ""
	sessionsRoot := ""

	if strings.TrimSpace(sessionFile) != "" {
		absoluteFile, err := filepath.Abs(filepath.Clean(sessionFile))
		if err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
		targetFile = absoluteFile
		if err := addExistingAncestorWatch(watcher, filepath.Dir(targetFile), watchedDirs); err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
	} else {
		codexRoot, err := codexHomePath(codexHome)
		if err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
		codexRoot, err = filepath.Abs(filepath.Clean(codexRoot))
		if err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
		sessionsRoot = filepath.Join(codexRoot, "sessions")
		if err := addExistingAncestorWatch(watcher, codexRoot, watchedDirs); err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
		if err := addExistingAncestorWatch(watcher, sessionsRoot, watchedDirs); err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
		if err := addCollectWatchTree(watcher, sessionsRoot, watchedDirs); err != nil {
			_ = watcher.Close()
			return nil, nil, nil, err
		}
	}

	changeCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var closeOnce sync.Once
	closeFn := func() error {
		var closeErr error
		closeOnce.Do(func() {
			closeErr = watcher.Close()
		})
		return closeErr
	}

	go func() {
		defer close(changeCh)
		defer close(errCh)

		var (
			timer   *time.Timer
			timerCh <-chan time.Time
		)
		resetDebounce := func() {
			if timer == nil {
				timer = time.NewTimer(collectWatchDebounce)
				timerCh = timer.C
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(collectWatchDebounce)
			timerCh = timer.C
		}
		stopDebounce := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timerCh = nil
		}

		for {
			select {
			case <-ctx.Done():
				stopDebounce()
				return
			case event, ok := <-watcher.Events:
				if !ok {
					stopDebounce()
					return
				}
				trigger, err := handleCollectWatchEvent(watcher, watchedDirs, event, targetFile, sessionsRoot)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					stopDebounce()
					return
				}
				if trigger {
					resetDebounce()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					stopDebounce()
					return
				}
				select {
				case errCh <- err:
				default:
				}
				stopDebounce()
				return
			case <-timerCh:
				timerCh = nil
				select {
				case changeCh <- struct{}{}:
				default:
				}
			}
		}
	}()

	return changeCh, errCh, closeFn, nil
}

func handleCollectWatchEvent(watcher *fsnotify.Watcher, watchedDirs map[string]struct{}, event fsnotify.Event, targetFile, sessionsRoot string) (bool, error) {
	name := filepath.Clean(event.Name)
	if name == "." || name == "" {
		return false, nil
	}

	if info, err := os.Stat(name); err == nil && info.IsDir() {
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
			if err := addCollectWatchTree(watcher, name, watchedDirs); err != nil {
				return false, err
			}
		}
		return false, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if strings.TrimSpace(targetFile) != "" {
		return sameCollectWatchPath(name, targetFile) && isCollectSessionMutation(event.Op), nil
	}
	if strings.TrimSpace(sessionsRoot) == "" || !isWithinRoot(sessionsRoot, name) {
		return false, nil
	}
	if !strings.EqualFold(filepath.Ext(name), ".jsonl") {
		return false, nil
	}
	return isCollectSessionMutation(event.Op), nil
}

func addCollectWatchTree(watcher *fsnotify.Watcher, root string, watchedDirs map[string]struct{}) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return addCollectWatchDir(watcher, filepath.Dir(root), watchedDirs)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		return addCollectWatchDir(watcher, path, watchedDirs)
	})
}

func addExistingAncestorWatch(watcher *fsnotify.Watcher, path string, watchedDirs map[string]struct{}) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if info.IsDir() {
				return addCollectWatchDir(watcher, current, watchedDirs)
			}
			return addCollectWatchDir(watcher, filepath.Dir(current), watchedDirs)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func addCollectWatchDir(watcher *fsnotify.Watcher, path string, watchedDirs map[string]struct{}) error {
	cleaned := filepath.Clean(path)
	if _, ok := watchedDirs[cleaned]; ok {
		return nil
	}
	if err := watcher.Add(cleaned); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	watchedDirs[cleaned] = struct{}{}
	return nil
}

func isCollectSessionMutation(op fsnotify.Op) bool {
	return op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Chmod) != 0
}

func sameCollectWatchPath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
