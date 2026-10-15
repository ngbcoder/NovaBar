package win

import (
	"fmt"
	"golang.org/x/sys/windows"
)

// TaskbarState manages hiding and unhiding the Windows taskbars safely.
type TaskbarState struct {
	primary   windows.HWND
	secondary []windows.HWND
	hidden    bool
}

// NewTaskbarState creates a new TaskbarState.
func NewTaskbarState() *TaskbarState {
	return &TaskbarState{
		secondary: make([]windows.HWND, 0),
	}
}

// Find finds the primary taskbar and any secondary monitor taskbars.
func (ts *TaskbarState) Find() error {
	primary, err := FindWindow("Shell_TrayWnd", "")
	if err != nil || primary == 0 {
		return fmt.Errorf("failed to locate primary taskbar (Shell_TrayWnd): %w", err)
	}
	ts.primary = primary

	// Clear secondary list before rediscovery
	ts.secondary = ts.secondary[:0]

	// Find secondary taskbars (Shell_SecondaryTrayWnd)
	var enumErr error
	cb := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		cName, err := GetClassName(hwnd)
		if err == nil && cName == "Shell_SecondaryTrayWnd" {
			ts.secondary = append(ts.secondary, hwnd)
		}
		return 1 // continue enumeration
	})

	enumErr = EnumWindows(cb, 0)
	if enumErr != nil {
		// Log or ignore if non-fatal
	}
	return nil
}

// Hide hides all identified taskbar windows.
func (ts *TaskbarState) Hide() error {
	if ts.primary == 0 {
		if err := ts.Find(); err != nil {
			return err
		}
	}

	ShowWindow(ts.primary, SW_HIDE)
	for _, sec := range ts.secondary {
		ShowWindow(sec, SW_HIDE)
	}
	ts.hidden = true
	return nil
}

// Show restores visibility to all taskbar windows.
func (ts *TaskbarState) Show() error {
	// Re-find in case handles changed
	_ = ts.Find()

	if ts.primary != 0 {
		ShowWindow(ts.primary, SW_SHOW)
	}
	for _, sec := range ts.secondary {
		ShowWindow(sec, SW_SHOW)
	}
	ts.hidden = false
	return nil
}

// IsHidden returns whether the taskbars are currently hidden.
func (ts *TaskbarState) IsHidden() bool {
	return ts.hidden
}

// ForceRestoreTaskbar provides a direct, unmanaged rescue restore of the taskbar.
func ForceRestoreTaskbar() {
	if h, err := FindWindow("Shell_TrayWnd", ""); err == nil && h != 0 {
		ShowWindow(h, SW_SHOW)
	}
	cb := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		cName, err := GetClassName(hwnd)
		if err == nil && cName == "Shell_SecondaryTrayWnd" {
			ShowWindow(hwnd, SW_SHOW)
		}
		return 1
	})
	_ = EnumWindows(cb, 0)
}
