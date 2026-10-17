package config

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadEvent describes a config change notification.
type ReloadEvent struct {
	// Path is the file that changed.
	Path string
	// Err is non-nil when the new content failed to parse or validate, in which case
	// the previous good config is still active.
	Err error
}

// Watcher reloads config.toml when it changes on disk.
//
// Editors frequently write via replace-then-rename, which breaks the inode watch. The
// watcher therefore debounces and re-establishes its watch on every event, and falls
// back to a periodic poll if the file ever disappears.
type Watcher struct {
	path string

	mu      sync.Mutex
	current Config

	watcher *fsnotify.Watcher
	events  chan ReloadEvent
	done    chan struct{}
	once    sync.Once
}

// NewWatcher starts watching the config file and returns the watcher.
//
// current is the config in effect at startup; it remains active until the first
// successful reload, so a bad edit never leaves the daemon configless.
func NewWatcher(cfg Config) (*Watcher, error) {
	path, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("could not create fsnotify watcher: %w", err)
	}

	w := &Watcher{
		path:    path,
		current: cfg,
		watcher: fsw,
		events:  make(chan ReloadEvent, 8),
		done:    make(chan struct{}),
	}

	if err := fsw.Add(path); err != nil {
		// A missing config is not fatal: Load writes one on first run, and the poll
		// loop below will pick it up once it exists.
		log.Printf("[NovaBar Config] Could not watch %s (%v); falling back to polling\n", path, err)
	}

	go w.run()

	return w, nil
}

// Events returns the channel carrying reload notifications.
func (w *Watcher) Events() <-chan ReloadEvent {
	return w.events
}

// Current returns the active config. Safe to call from any goroutine.
func (w *Watcher) Current() Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}

// Path returns the watched config path.
func (w *Watcher) Path() string {
	return w.path
}

// debounce is how long to wait after the last filesystem event before reloading.
// Windows editors emit several events per save.
const debounce = 150 * time.Millisecond

// pollInterval is the fallback resync period, used when the watch cannot be
// established (rename-based saves can drop it).
const pollInterval = 5 * time.Second

func (w *Watcher) run() {
	var (
		timer     *time.Timer
		timerC    <-chan time.Time
		lastStamp = fileStamp(w.path)
	)

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	for {
		select {
		case <-w.done:
			return

		case ev, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if ev.Name != w.path && filepath.Clean(ev.Name) != filepath.Clean(w.path) {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				timer.Reset(debounce)
			}
			timerC = timer.C
			// Re-arm the watch: a replace-based save moves the inode out from under it.
			_ = w.watcher.Add(w.path)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[NovaBar Config] watcher error: %v\n", err)

		case <-timerC:
			timerC = nil
			w.reload()

		case <-poll.C:
			stamp := fileStamp(w.path)
			if stamp != lastStamp {
				lastStamp = stamp
				_ = w.watcher.Add(w.path)
				w.reload()
			}
		}
	}
}

// reload re-reads the config file and swaps it in on success.
func (w *Watcher) reload() {
	stamp := fileStamp(w.path)
	if stamp == "" {
		// File vanished (mid-rename). Keep the current config; the poll loop will
		// pick it up once it reappears.
		return
	}

	cfg, err := Load()
	if err != nil {
		log.Printf("[NovaBar Config] Reload failed, keeping previous config: %v\n", err)
		w.emit(ReloadEvent{Path: w.path, Err: err})
		return
	}

	w.mu.Lock()
	changed := cfg.Tiling != w.current.Tiling ||
		cfg.General.HideTaskbar != w.current.General.HideTaskbar ||
		cfg.Hotkeys != w.current.Hotkeys ||
		!slices.Equal(cfg.Bars, w.current.Bars) ||
		len(cfg.Rules) != len(w.current.Rules)
	w.current = cfg
	w.mu.Unlock()

	if changed {
		log.Printf("[NovaBar Config] Reloaded %s\n", w.path)
	}
	w.emit(ReloadEvent{Path: w.path, Err: nil})
}

func (w *Watcher) emit(ev ReloadEvent) {
	select {
	case w.events <- ev:
	default:
	}
}

// Close stops the watcher. Safe to call more than once.
func (w *Watcher) Close() {
	w.once.Do(func() {
		close(w.done)
		_ = w.watcher.Close()
	})
}
