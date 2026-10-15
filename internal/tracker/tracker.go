package tracker

import (
	"sync"

	"github.com/novabar-shell/novabar/internal/win"
	"golang.org/x/sys/windows"
)

// WindowInfo encapsulates window state tracked by the daemon.
type WindowInfo struct {
	HWnd      windows.HWND
	PID       uint32
	Title     string
	ClassName string
	Rect      win.RECT
	IsCloaked bool
	IsVisible bool
}

// WindowTracker manages windows discovered via hooks and enumeration.
type WindowTracker struct {
	mu      sync.RWMutex
	windows map[windows.HWND]*WindowInfo
}

// NewWindowTracker creates a tracker instance.
func NewWindowTracker() *WindowTracker {
	return &WindowTracker{
		windows: make(map[windows.HWND]*WindowInfo),
	}
}

// IsManageable determines if a window qualifies for shell tiling and tracking.
func (t *WindowTracker) IsManageable(hwnd windows.HWND) bool {
	if hwnd == 0 || !win.IsWindowVisible(hwnd) {
		return false
	}

	// Filter cloaked windows (UWP background suspended, hidden desktops)
	if win.IsWindowCloaked(hwnd) {
		return false
	}

	// Filter tool windows and non-app surfaces
	exStyle, err := win.GetWindowLongPtr(hwnd, win.GWL_EXSTYLE)
	if err == nil && (exStyle&win.WS_EX_TOOLWINDOW != 0) && (exStyle&win.WS_EX_APPWINDOW == 0) {
		return false
	}

	className, _ := win.GetClassName(hwnd)
	switch className {
	case "Progman", "WorkerW", "Shell_TrayWnd", "Shell_SecondaryTrayWnd":
		return false
	}

	return true
}

// Refresh re-enumerates top-level windows.
func (t *WindowTracker) Refresh() {
	t.mu.Lock()
	defer t.mu.Unlock()

	clear(t.windows)

	cb := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if t.IsManageable(hwnd) {
			pid, _ := win.GetWindowPID(hwnd)
			title, _ := win.GetWindowText(hwnd)
			className, _ := win.GetClassName(hwnd)
			bounds, _ := win.GetExtendedFrameBounds(hwnd)

			t.windows[hwnd] = &WindowInfo{
				HWnd:      hwnd,
				PID:       pid,
				Title:     title,
				ClassName: className,
				Rect:      bounds,
				IsVisible: true,
			}
		}
		return 1
	})

	_ = win.EnumWindows(cb, 0)
}

// Count returns the number of managed windows currently tracked.
func (t *WindowTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.windows)
}

// GetWindowHandles returns a list of currently manageable window HWNDs.
func (t *WindowTracker) GetWindowHandles() []windows.HWND {
	t.mu.RLock()
	defer t.mu.RUnlock()
	handles := make([]windows.HWND, 0, len(t.windows))
	for h := range t.windows {
		handles = append(handles, h)
	}
	return handles
}
