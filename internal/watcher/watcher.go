// Package watcher keeps the index in sync with the filesystem via fsnotify.
//
// Events are debounced: editors often fire several WRITEs as they save, and
// we want to reindex once the dust settles rather than on every keystroke.
package watcher

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/yasu/vault-search/internal/indexer"
)

type Watcher struct {
	root string
	idx  *indexer.Indexer
	fsw  *fsnotify.Watcher
}

func New(root string, idx *indexer.Indexer) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{root: root, idx: idx, fsw: fsw}
	if err := w.addRecursive(root); err != nil {
		fsw.Close()
		return nil, err
	}
	return w, nil
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}

func (w *Watcher) Run(ctx context.Context) {
	defer w.fsw.Close()
	const debounce = 300 * time.Millisecond
	pending := make(map[string]time.Time)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// New subdirectory: start watching it too.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.addRecursive(ev.Name)
					continue
				}
			}
			if !isMarkdown(ev.Name) {
				continue
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				if err := w.idx.Delete(ev.Name); err != nil {
					log.Printf("watcher: delete %s: %v", ev.Name, err)
				}
				delete(pending, ev.Name)
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				pending[ev.Name] = time.Now()
			}

		case now := <-ticker.C:
			for path, t := range pending {
				if now.Sub(t) < debounce {
					continue
				}
				if err := w.idx.IndexFile(path); err != nil {
					log.Printf("watcher: index %s: %v", path, err)
				}
				delete(pending, path)
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher: %v", err)
		}
	}
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}
