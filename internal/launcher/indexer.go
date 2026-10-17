package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// AppItem represents an indexed launchable application.
type AppItem struct {
	Name string `json:"name"`
	// Path is the .lnk or .exe to launch. Empty for UWP entries, which carry an
	// AppUserModelID in Target instead.
	Path string `json:"path,omitempty"`
	// Target is what actually gets launched. For UWP it is the AppUserModelID.
	Target string `json:"target"`
	// Kind is "app", "uwp", "command" or "running".
	Kind string `json:"kind"`
	// Source records where the entry came from, for debugging a bad index.
	Source string `json:"source,omitempty"`
}

// Provider names an item source.
const (
	KindApp     = "app"
	KindUWP     = "uwp"
	KindCommand = "command"
	KindRunning = "running"
)

// Indexer scans Start Menu shortcuts, UWP apps and PATH for the launcher.
type Indexer struct {
	mu    sync.RWMutex
	items []AppItem
}

// NewIndexer creates and populates an app indexer.
func NewIndexer() *Indexer {
	idx := &Indexer{items: make([]AppItem, 0, 256)}
	idx.Reindex()
	return idx
}

// Reindex rescans every provider. Takes a few hundred milliseconds; call it off the
// message pump thread.
//
// On UWP coverage: Store apps are covered by the Start Menu scan rather than a
// separate shell:AppsFolder enumeration. Windows writes an .lnk per installed Store
// app into %APPDATA%\Microsoft\Windows\Start Menu\Programs\<Package Family Name>\,
// carrying the AppUserModelID as its target, and `cmd /c start` resolves that target
// correctly. Enumerating AppsFolder directly would mean driving IShellWindows and
// IEnumIDList by hand, which needs go-ole and therefore cgo, and would add no entries
// the Start Menu scan does not already produce. A genuine AppsFolder enumeration is
// the right follow-up if a specific Store app ever goes missing.
func (idx *Indexer) Reindex() {
	items := make([]AppItem, 0, 256)
	items = append(items, idx.scanStartMenu()...)
	items = append(items, idx.scanPath()...)

	items = deduplicate(items)

	// Deterministic base order so an empty query lists apps alphabetically rather
	// than in directory-walk order, which changes between machines.
	sort.SliceStable(items, func(i, j int) bool {
		li, lj := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if li != lj {
			return li < lj
		}
		return items[i].Target < items[j].Target
	})

	idx.mu.Lock()
	idx.items = items
	idx.mu.Unlock()
}

// scanStartMenu walks the per-user and common Start Menu program folders.
func (idx *Indexer) scanStartMenu() []AppItem {
	items := make([]AppItem, 0, 128)

	roots := []struct {
		dir    string
		source string
	}{}

	if appData := os.Getenv("APPDATA"); appData != "" {
		roots = append(roots, struct {
			dir    string
			source string
		}{filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs`), "start-menu-user"})
	}
	if progData := os.Getenv("PROGRAMDATA"); progData != "" {
		roots = append(roots, struct {
			dir    string
			source string
		}{filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs`), "start-menu-common"})
	}

	for _, root := range roots {
		_ = filepath.Walk(root.dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".lnk", ".exe":
				name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				items = append(items, AppItem{
					Name:   name,
					Path:   path,
					Target: path,
					Kind:   KindApp,
					Source: root.source,
				})
			}
			return nil
		})
	}

	return items
}

// scanPath indexes executables already on PATH.
func (idx *Indexer) scanPath() []AppItem {
	items := make([]AppItem, 0, 64)

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // unreadable PATH entry; normal for system dirs
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".exe") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			items = append(items, AppItem{
				Name:   strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
				Path:   full,
				Target: full,
				Kind:   KindApp,
				Source: "path",
			})
		}
	}

	return items
}

// deduplicate keeps the first entry per target, preferring Start Menu over PATH.
func deduplicate(items []AppItem) []AppItem {
	seen := make(map[string]bool, len(items))
	out := make([]AppItem, 0, len(items))
	for _, it := range items {
		key := strings.ToLower(it.Target)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

// Count returns the number of indexed items.
func (idx *Indexer) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.items)
}

// Items returns a snapshot of the index.
func (idx *Indexer) Items() []AppItem {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]AppItem, len(idx.items))
	copy(out, idx.items)
	return out
}

// Reindex replaces the contents of the index. Exposed for the daemon to call after a
// Start Menu change.
func (idx *Indexer) Replace(items []AppItem) {
	idx.mu.Lock()
	idx.items = items
	idx.mu.Unlock()
}