package win

import (
	"fmt"
	"log"

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

// Find locates the primary taskbar and every secondary monitor taskbar.
//
// Must be called with no EnumProc active: it installs a handler on the shared
// EnumWindows trampoline for the duration of the walk.
func (ts *TaskbarState) Find() error {
	primary, err := FindWindow("Shell_TrayWnd", "")
	if err != nil || primary == 0 {
		return fmt.Errorf("failed to locate primary taskbar (Shell_TrayWnd): %w", err)
	}
	ts.primary = primary

	secondary := make([]windows.HWND, 0, 4)
	SetEnumWindowsHandler(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if cName, err := GetClassName(hwnd); err == nil && cName == "Shell_SecondaryTrayWnd" {
			secondary = append(secondary, hwnd)
		}
		return 1 // continue enumeration
	})
	EnumWindows(0)
	ClearEnumWindowsHandler()

	ts.secondary = secondary
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
	// Re-find in case handles changed across a monitor or shell restart.
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
//
// This is the last line of defence: it does not require the daemon to be running and
// does not touch AppBar registration. It deliberately re-finds the taskbar from
// scratch rather than trusting cached handles, because the caller is usually a
// recovery path invoked precisely when cached state is suspect.
func ForceRestoreTaskbar() {
	if h, err := FindWindow("Shell_TrayWnd", ""); err == nil && h != 0 {
		ShowWindow(h, SW_SHOW)
	}
	SetEnumWindowsHandler(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if cName, err := GetClassName(hwnd); err == nil && cName == "Shell_SecondaryTrayWnd" {
			ShowWindow(hwnd, SW_SHOW)
		}
		return 1
	})
	EnumWindows(0)
	ClearEnumWindowsHandler()

	log.Println("[NovaBar Taskbar] Force-restored native taskbars.")
}

// WaitForProcess blocks until the process with the given id exits, then returns its
// exit code. Returns false immediately if the id cannot be opened.
//
// This is the watchdog's core primitive: it blocks on the parent process and fires
// only when the parent is genuinely gone.
func WaitForProcess(pid uint32) (exitCode uint32, ok bool) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)

	wait, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil {
		return 0, false
	}
	if wait != uint32(windows.WAIT_OBJECT_0) {
		return 0, false
	}

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return 0, false
	}
	return code, true
}

// IsProcessAlive reports whether a process id currently refers to a live process.
func IsProcessAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}