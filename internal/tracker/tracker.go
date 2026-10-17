package tracker

import (
	"log"
	"sort"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

// WindowInfo encapsulates window state tracked by the daemon.
//
// JSON tags are explicit because widgets consume this directly. Without them encoding
// would emit the Go field names, and a widget asking for `title` would silently get
// undefined while the field sat right there as "Title".
type WindowInfo struct {
	HWnd      windows.HWND `json:"hwnd"`
	PID       uint32       `json:"pid"`
	ProcName  string       `json:"proc_name"`
	Title     string       `json:"title"`
	ClassName string       `json:"class_name"`
	Rect      win.RECT     `json:"rect"`
	IsCloaked bool         `json:"cloaked"`
	IsVisible bool         `json:"visible"`
}

// shellClasses are top-level windows that belong to the Windows shell. They must
// never be tiled or tracked.
var shellClasses = map[string]bool{
	"Progman":                    true,
	"WorkerW":                    true,
	"Shell_TrayWnd":              true,
	"Shell_SecondaryTrayWnd":     true,
	"Windows.UI.Core.CoreWindow": true,
}

// WindowTracker maintains the set of manageable top-level windows.
//
// Ordering is insertion order and is stable across refreshes. An earlier Go-map
// implementation returned windows in randomized order, so every retile reshuffled
// the desktop unpredictably.
type WindowTracker struct {
	mu      sync.RWMutex
	order   []windows.HWND
	windows map[windows.HWND]*WindowInfo
	procs   map[uint32]string

	// events carries coalesced window-lifecycle notifications to the daemon's event
	// pump. The WinEvent hook must stay cheap, so it only enqueues here.
	events chan WinEvent
}

// WinEvent is a raw window-lifecycle notification delivered off the hook callback.
type WinEvent struct {
	HWnd  windows.HWND
	Kind  EventKind
	Event uint32
}

// EventKind classifies a window lifecycle event.
type EventKind uint8

const (
	// EventShow means a window appeared or was shown.
	EventShow EventKind = iota
	// EventHide means a window was hidden.
	EventHide
	// EventDestroy means a window was destroyed.
	EventDestroy
	// EventForeground means focus moved to a different window.
	EventForeground
	// EventMoveResize means a user or program moved or resized a window.
	EventMoveResize
)

// eventQueueDepth bounds the event channel. Overflow is dropped rather than blocking
// the hook, because a full queue already means a refresh is pending.
const eventQueueDepth = 256

// NewWindowTracker creates a tracker instance.
func NewWindowTracker() *WindowTracker {
	return &WindowTracker{
		windows: make(map[windows.HWND]*WindowInfo),
		order:   make([]windows.HWND, 0, 32),
		procs:   make(map[uint32]string, 64),
		events:  make(chan WinEvent, eventQueueDepth),
	}
}

// Events returns the channel carrying coalesced window lifecycle notifications.
func (t *WindowTracker) Events() <-chan WinEvent {
	return t.events
}

// Notify enqueues a lifecycle event for the pump. Never blocks: if the queue is full
// the event is dropped, which is safe because the pump re-enumerates on every wakeup
// and an extra refresh is idempotent.
func (t *WindowTracker) Notify(ev WinEvent) {
	select {
	case t.events <- ev:
	default:
	}
}

// IsManageable determines if a window qualifies for shell tiling and tracking.
func (t *WindowTracker) IsManageable(hwnd windows.HWND) bool {
	if hwnd == 0 || !win.IsValid(hwnd) || !win.IsWindowVisible(hwnd) {
		return false
	}

	// Cloaked windows are suspended UWP apps or windows on another virtual desktop.
	if win.IsWindowCloaked(hwnd) {
		return false
	}

	// Tool windows are overlays, palettes and tooltips. WS_EX_APPWINDOW overrides
	// WS_EX_TOOLWINDOW, so a real app that sets both is still managed.
	exStyle, err := win.GetWindowLongPtr(hwnd, win.GWL_EXSTYLE)
	if err == nil && (exStyle&win.WS_EX_TOOLWINDOW != 0) && (exStyle&win.WS_EX_APPWINDOW == 0) {
		return false
	}

	className, _ := win.GetClassName(hwnd)
	if shellClasses[className] {
		return false
	}

	// Windows with no title are almost always message boxes' owner windows or
	// transient helper surfaces.
	if title, _ := win.GetWindowText(hwnd); title == "" {
		return false
	}

	return true
}

// processName resolves and caches the executable name for a pid.
func (t *WindowTracker) processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	t.mu.RLock()
	if name, ok := t.procs[pid]; ok {
		t.mu.RUnlock()
		return name
	}
	t.mu.RUnlock()

	name := win.ProcessName(pid)

	t.mu.Lock()
	t.procs[pid] = name
	t.mu.Unlock()
	return name
}

// Refresh re-enumerates top-level windows, preserving the existing order for
// windows that are still present and appending genuinely new ones at the end.
//
// Must not be called from inside a WinEvent hook callback or an EnumProc: it walks
// windows and installs a handler on the shared EnumWindows trampoline.
func (t *WindowTracker) Refresh() {
	found := make(map[windows.HWND]*WindowInfo, 64)

	win.SetEnumWindowsHandler(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if !t.IsManageable(hwnd) {
			return 1
		}
		pid, _ := win.GetWindowPID(hwnd)
		title, _ := win.GetWindowText(hwnd)
		className, _ := win.GetClassName(hwnd)
		bounds, _ := win.GetExtendedFrameBounds(hwnd)
		procName := t.processName(pid)

		found[hwnd] = &WindowInfo{
			HWnd:      hwnd,
			PID:       pid,
			ProcName:  procName,
			Title:     title,
			ClassName: className,
			Rect:      bounds,
			IsVisible: true,
		}
		return 1
	})
	win.EnumWindows(0)
	win.ClearEnumWindowsHandler()

	t.mu.Lock()
	defer t.mu.Unlock()

	// Preserve prior ordering for surviving windows, append newcomers.
	newOrder := make([]windows.HWND, 0, len(found))
	kept := make(map[windows.HWND]bool, len(found))
	for _, h := range t.order {
		if _, ok := found[h]; ok && !kept[h] {
			newOrder = append(newOrder, h)
			kept[h] = true
		}
	}

	// Anything left is genuinely new. Sort by handle for determinism so two runs
	// starting from the same state agree on the order.
	newcomers := make([]windows.HWND, 0, len(found))
	for h := range found {
		if !kept[h] {
			newcomers = append(newcomers, h)
		}
	}
	sort.Slice(newcomers, func(i, j int) bool { return newcomers[i] < newcomers[j] })
	newOrder = append(newOrder, newcomers...)

	// Drop process-name cache entries for pids we no longer see.
	if len(t.procs) > 512 {
		live := make(map[uint32]bool, len(found))
		for _, wi := range found {
			live[wi.PID] = true
		}
		for pid := range t.procs {
			if !live[pid] {
				delete(t.procs, pid)
			}
		}
	}

	t.order = newOrder
	t.windows = found
}

// Count returns the number of managed windows currently tracked.
func (t *WindowTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.windows)
}

// GetWindowHandles returns manageable window HWNDs in stable insertion order.
func (t *WindowTracker) GetWindowHandles() []windows.HWND {
	t.mu.RLock()
	defer t.mu.RUnlock()
	handles := make([]windows.HWND, len(t.order))
	copy(handles, t.order)
	return handles
}

// Snapshot returns a copy of the tracked window set in stable order.
func (t *WindowTracker) Snapshot() []WindowInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]WindowInfo, 0, len(t.order))
	for _, h := range t.order {
		if wi, ok := t.windows[h]; ok {
			out = append(out, *wi)
		}
	}
	return out
}

// Get returns tracked metadata for one window.
func (t *WindowTracker) Get(hwnd windows.HWND) (WindowInfo, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	wi, ok := t.windows[hwnd]
	if !ok {
		return WindowInfo{}, false
	}
	return *wi, true
}

// AssignWorkspace attaches every tracked window to the given workspace id. Used on
// startup so existing windows land in workspace 1 rather than in no workspace at all.
func (t *WindowTracker) AssignWorkspace(assign func(windows.HWND, int)) {
	t.mu.RLock()
	handles := make([]windows.HWND, len(t.order))
	copy(handles, t.order)
	t.mu.RUnlock()

	for _, h := range handles {
		assign(h, 1)
	}
}

// LogSummary prints a one-line count of tracked windows. Used at startup.
func (t *WindowTracker) LogSummary() {
	t.mu.RLock()
	defer t.mu.RUnlock()
	log.Printf("[NovaBar Tracker] %d manageable window(s)\n", len(t.windows))
}
