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

// Hide hides all identified taskbar windows and forces the work area to be recomputed.
//
// The refresh is what makes a bottom AppBar possible. ShowWindow(SW_HIDE) alone leaves
// the shell's cached work area unchanged, so the strip the taskbar occupied stays
// excluded and a bottom bar asking for it is refused with nothing granted.
func (ts *TaskbarState) Hide() error {
	if ts.primary == 0 {
		if err := ts.Find(); err != nil {
			return err
		}
	}

	// Capture the pre-hide work area so the refresh can be waited on.
	before, _ := PrimaryWorkArea()

	ShowWindow(ts.primary, SW_HIDE)
	for _, sec := range ts.secondary {
		ShowWindow(sec, SW_HIDE)
	}
	ts.hidden = true

	RefreshWorkArea()
	if after, settled := WorkAreaAfterInsets(before); settled {
		log.Printf("[NovaBar Taskbar] Taskbars hidden; work area grew from %d to %d\n", before, after)
	} else {
		log.Printf("[NovaBar Taskbar] Taskbars hidden; work area unchanged at %d\n", before)
	}
	return nil
}

// Show restores visibility to all taskbar windows and refreshes the work area.
func (ts *TaskbarState) Show() error {
	before, _ := PrimaryWorkArea()

	// Re-find in case handles changed across a monitor or shell restart.
	_ = ts.Find()

	if ts.primary != 0 {
		ShowWindow(ts.primary, SW_SHOW)
	}
	for _, sec := range ts.secondary {
		ShowWindow(sec, SW_SHOW)
	}
	ts.hidden = false

	RefreshWorkArea()
	if after, settled := WorkAreaAfterInsets(before); settled {
		log.Printf("[NovaBar Taskbar] Taskbars restored; work area is now %d\n", after)
	}
	return nil
}

// IsHidden returns whether the taskbars are currently hidden.
func (ts *TaskbarState) IsHidden() bool {
	return ts.hidden
}

// ForceRestoreTaskbar is the last line of defence: it restores the taskbar and forces
// the whole work area back, without needing the daemon or trusting any cached state.
//
// This is what runs in the recovery path, so it does everything rather than the minimum:
//   - re-shows the taskbar, found from scratch rather than a cached handle
//   - calls RestoreFullWorkArea, which is the only thing that clears a stale AppBar
//     reservation left behind by a process that was killed without running its cleanup
//   - nudges the shell into a layout pass and broadcasts the change to applications
//
// Callers are the watchdog and `shellctl --rescue`. Running the taskbar again is
// harmless when it was never hidden, so it is unconditional: restoring a desktop that
// did not need it costs nothing, and failing to restore one that did is unrecoverable
// without a reboot.
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

	// Absolute reset of the work area. This is the step that matters: without it a
	// desktop left shrunk by a dead process's AppBar registration stays shrunk.
	if err := RestoreFullWorkArea(); err != nil {
		log.Printf("[NovaBar Taskbar] WARNING: could not reset the work area: %v\n", err)
	} else {
		log.Println("[NovaBar Taskbar] Work area reset to the full primary display.")
	}

	RefreshWorkArea()
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
