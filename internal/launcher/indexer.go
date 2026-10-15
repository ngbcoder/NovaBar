package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AppItem represents an indexed launchable application or command.
type AppItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// Indexer scans Start Menu shortcuts and paths to provide fast fuzzy search.
type Indexer struct {
	mu    sync.RWMutex
	items []AppItem
}

// NewIndexer creates and populates an app indexer.
func NewIndexer() *Indexer {
	idx := &Indexer{
		items: make([]AppItem, 0),
	}
	idx.Reindex()
	return idx
}

// Reindex scans common Windows Start Menu locations without requiring elevation.
func (idx *Indexer) Reindex() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.items = idx.items[:0]

	// User Start Menu
	if appData := os.Getenv("APPDATA"); appData != "" {
		userStart := filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs`)
		idx.scanDir(userStart)
	}

	// System/Common Start Menu
	if progData := os.Getenv("PROGRAMDATA"); progData != "" {
		commonStart := filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs`)
		idx.scanDir(commonStart)
	}
}

func (idx *Indexer) scanDir(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".lnk" || ext == ".exe" {
			name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
			idx.items = append(idx.items, AppItem{
				Name: name,
				Path: path,
			})
		}
		return nil
	})
}

// Search performs a case-insensitive prefix and substring match.
func (idx *Indexer) Search(query string, limit int) []AppItem {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if query == "" {
		if len(idx.items) > limit {
			return idx.items[:limit]
		}
		return idx.items
	}

	q := strings.ToLower(query)
	var matches []AppItem

	for _, item := range idx.items {
		nameLower := strings.ToLower(item.Name)
		if strings.Contains(nameLower, q) {
			matches = append(matches, item)
			if limit > 0 && len(matches) >= limit {
				break
			}
		}
	}

	return matches
}
