package win

import (
	"fmt"
	"log"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TaskbarState manages hiding and unhiding the Windows taskbars safely.
//
// The zero-value is not usable; call NewTaskbarState.
type TaskbarState struct {
	mu        sync.Mutex
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
	EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if cName, err := GetClassName(hwnd); err == nil && cName == "Shell_SecondaryTrayWnd" {
			secondary = append(secondary, hwnd)
		}
		return 1 // continue enumeration
	}, 0)

	ts.secondary = secondary
	return nil
}

// Hide hides all identified taskbar windows and forces the work area to be recomputed.
//
// The refresh is what makes a bottom AppBar possible. ShowWindow(SW_HIDE) alone leaves
// the shell's cached work area unchanged, so the strip the taskbar occupied stays
// excluded and a bottom bar asking for it is refused with nothing granted.
//
// The order of the steps is load bearing, and it is the opposite of the obvious one.
//
// The shell does not accept a hidden taskbar. Whenever the work area changes, the
// shell lays the taskbar out afresh, and that layout restores the window: visible,
// back at the bottom of the screen, within a second of being put away. A hide that
// ends with a work-area write therefore undoes itself, which is exactly what this
// function used to do and exactly what the user saw: a taskbar that came back.
//
// So the work area is released first and the windows are put away last. The shell is
// left something to react to, and whatever it does with it, the last word belongs to
// the hide.
//
// The shell's reaction can still arrive late, so the caller keeps a guard running
// that puts the window away again without touching the work area.
func (ts *TaskbarState) Hide() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.primary == 0 {
		if err := ts.Find(); err != nil {
			return err
		}
	}

	// Capture the work area before hiding so the change can be reported honestly.
	before, _ := PrimaryWorkArea()

	// Release the work area first. Hiding the taskbar window does not make Windows
	// give its reservation back on Windows 11: the window disappears but the work
	// area keeps the pixels it was holding. The shell then lays out against a work
	// area with a phantom strip at the bottom, which is why a floating dock hovers
	// above a gap where the taskbar used to be.
	//
	// Releasing it means writing the full screen as the work area outright. Done here
	// rather than left to RefreshWorkArea because that only nudges the taskbar geometry
	// and broadcasts, which this shell has already seen ignored.
	RefreshWorkArea()
	if err := SetFullWorkArea(false); err != nil {
		log.Printf("[NovaBar Taskbar] Taskbars hidden but the work area could not be released: %v\n", err)
	}
	RefreshWorkArea()

	// Now put the windows away, last. Everything above provokes the shell into
	// laying the taskbar out again, and this is the step that has to come after.
	for _, h := range ts.all() {
		hideOne(h)
	}
	ts.hidden = true

	after, _ := PrimaryWorkArea()
	if after > before {
		log.Printf("[NovaBar Taskbar] Taskbars hidden; work area grew from %d to %d\n", before, after)
	} else {
		// Reporting success here would be actively misleading: the user sees a taskbar.
		log.Printf("[NovaBar Taskbar] Taskbars hidden but the work area is still %d; "+
			"Windows did not release the taskbar's reservation\n", after)
	}
	return nil
}

// all returns the primary taskbar plus every secondary one.
func (ts *TaskbarState) all() []windows.HWND {
	out := make([]windows.HWND, 0, len(ts.secondary)+1)
	if ts.primary != 0 {
		out = append(out, ts.primary)
	}
	return append(out, ts.secondary...)
}

// hideOne hides a single taskbar window.
//
// SW_HIDE alone is not enough on Windows 11: the shell re-shows its taskbar, sometimes
// within the same frame, so a shell that only calls it appears to hide the taskbar while
// leaving it fully visible. Two further steps make it stick.
//
// SetWindowPos with SWP_HIDEWINDOW goes through a different code path in the shell, which
// is what survives its re-show. Moving the window off-screen is the fallback a visibility
// change cannot undo: even if the shell shows it again, it is not where anyone can see it.
// Deliberately reversible, and Show restores the position, because rewriting the taskbar's
// location permanently would outlive NovaBar.
func hideOne(hwnd windows.HWND) {
	ShowWindow(hwnd, SW_HIDE)

	_ = SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE|SWP_HIDEWINDOW)

	// Collapse the taskbar to zero height and park it off-screen.
	//
	// The height is the part that matters. Windows 11 recomputes the work area from the
	// taskbar's geometry, so a taskbar that is merely hidden, or merely moved off-screen,
	// keeps reserving the strip it was occupying: the dock then floats above a phantom gap.
	// Zero height is the only geometry that gives the pixels back.
	//
	// The width is kept at a token value rather than zero because a zero-width window is
	// treated as unresizable by the shell and can be recreated at its original size.
	_ = SetWindowPos(hwnd, 0, -32000, -32000, 1, 0,
		SWP_NOZORDER|SWP_NOACTIVATE|SWP_HIDEWINDOW)
}

// showOne is the inverse of hideOne.
//
// The move-back matters more than it looks: the watchdog calls ForceRestoreTaskbar after a
// crash, and a plain ShowWindow there would leave the taskbar restored to visibility but
// parked off-screen at -32000,-32000. The user would still have no desktop, which is the
// one outcome this whole subsystem exists to prevent.
//
// The position is left to the shell wherever it can be. The shell places its own
// taskbar, and it knows things this code does not: whether the desktop is in
// auto-hide mode, where the taskbar belongs on each monitor, and how far off-screen
// it parks itself. Moving it here overrides all of that, and the result is a taskbar
// in the wrong place that the shell corrects on its next layout pass anyway.
//
// A taskbar parked off-screen is the exception: that is not a position the user can
// reach, so it is put back at the bottom of its monitor.
func showOne(hwnd windows.HWND) {
	_ = SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE|SWP_SHOWWINDOW)
	ShowWindow(hwnd, SW_SHOW)

	rect, err := GetWindowRect(hwnd)
	if err != nil || rect.Left > -1000 || rect.Top > -1000 {
		return
	}

	// Parked off-screen. Put it at the bottom of its monitor, which is where the
	// shell would put it.
	hMon := MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST)
	if hMon == 0 {
		return
	}
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if !GetMonitorInfo(hMon, &mi) || mi.RcMonitor.Height() == 0 {
		return
	}

	width, height := rect.Width(), rect.Height()
	if width <= 1 {
		width = mi.RcMonitor.Width()
	}
	if height <= 1 {
		height = defaultTaskbarHeight
	}

	_ = SetWindowPos(hwnd, 0, mi.RcMonitor.Left, mi.RcMonitor.Bottom-height, width, height,
		SWP_NOZORDER|SWP_NOACTIVATE|SWP_SHOWWINDOW)
}

// HideWindows re-hides the taskbar windows without touching the work area.
//
// This is the guard's primitive, and it is separate from Hide on purpose. Hiding the
// windows and releasing the work area are two different jobs, and the second one
// provokes the shell into showing the first one again: rewriting the work area makes
// the shell lay the taskbar out afresh, and a fresh layout restores the window.
//
// So the guard re-hides the windows and leaves the work area alone.
//
// It reports whether it had to act, so the caller can stay quiet when there is
// nothing to do.
func (ts *TaskbarState) HideWindows() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// The shell replaces the taskbar window when it lays the desktop out again, so
	// the handles have to be refreshed when they go stale.
	//
	// This is not a detail. A stale handle reads as "not visible", so a guard
	// holding one believes there is nothing to do while the taskbar is on screen.
	// The shell recreates the window every time the work area changes, which is
	// exactly when the taskbar comes back, so a guard that trusts its handles
	// stops working at the first bar resize.
	if ts.stale() {
		_ = ts.Find()
	}

	acted := false
	for _, hwnd := range ts.all() {
		if !IsWindowVisible(hwnd) {
			continue
		}
		hideOne(hwnd)
		acted = true
	}

	if acted {
		ts.hidden = true
	}
	return acted
}

// stale reports whether any of the known taskbar handles no longer refers to a live
// window.
func (ts *TaskbarState) stale() bool {
	if ts.primary == 0 {
		return true
	}
	if !IsValid(ts.primary) {
		return true
	}
	for _, hwnd := range ts.secondary {
		if !IsValid(hwnd) {
			return true
		}
	}
	return false
}

// Show restores visibility to all taskbar windows and refreshes the work area.
func (ts *TaskbarState) Show() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	before, _ := PrimaryWorkArea()

	// Re-find in case handles changed across a monitor or shell restart.
	_ = ts.Find()

	for _, h := range ts.all() {
		// Undo the off-screen parking before showing, otherwise the taskbar reappears in
		// the top-left corner instead of at the bottom.
		showOne(h)
	}
	ts.hidden = false

	// The work area has to come back to the value Windows ships with, or the shell
	// keeps the taskbar below the bottom edge of the screen and the user gets a
	// taskbar they cannot see.
	if err := restoreTaskbars(); err != nil {
		log.Printf("[NovaBar Taskbar] Could not restore the work area: %v\n", err)
	}

	RefreshWorkArea()
	if after, settled := WorkAreaAfterInsets(before); settled {
		log.Printf("[NovaBar Taskbar] Taskbars restored; work area is now %d\n", after)
	}
	return nil
}

// IsHidden returns whether the taskbars are currently hidden.
func (ts *TaskbarState) IsHidden() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.hidden
}

// restoreTaskbars brings the taskbars and the work area back to the state Windows
// ships with.
//
// The order is the whole point, because each step undoes the previous one:
//
//   - Showing a taskbar is not enough on its own. The shell places the taskbar
//     relative to the work area, so a taskbar shown while the work area is the full
//     screen is placed below the bottom edge of the screen: visible as a window,
//     invisible to the person who needs it.
//   - Writing the full screen is not enough either. It is the only value that clears
//     a stale AppBar reservation, but it is also what tells the shell there is no
//     taskbar, and the shell parks the taskbar off-screen in response.
//   - Writing the work area Windows ships with is what puts the taskbar back where
//     it belongs, but only once the taskbar is where it belongs.
//
// So: show and place them, clear the stale reservations, place them again, and
// finally write the work area the shell expects.
func restoreTaskbars() error {
	// Show every taskbar and put it at the bottom of its monitor.
	//
	// The order matters, and it is not the obvious one. The full screen is the
	// value that clears a stale AppBar reservation, and it was used here for
	// years, but writing it is what makes the shell hide the taskbar: told that
	// the whole screen is available, the shell concludes there is no room for a
	// taskbar and hides the one there is. Its reaction to that write lands late,
	// so the taskbar is put back first and the write, if any, comes after.
	showAllTaskbars()

	// The work area Windows ships with, persisted so it survives a reboot.
	if err := RestoreNormalWorkArea(); err != nil {
		log.Printf("[NovaBar Taskbar] WARNING: could not reset the work area: %v\n", err)
		return err
	}

	// Let the shell finish rearranging before the last word is said.
	WaitForWorkAreaSettle()

	log.Println("[NovaBar Taskbar] Work area reset to the full primary display.")
	return nil
}

// showAllTaskbars shows every taskbar window and puts it at the bottom of its monitor.
func showAllTaskbars() {
	if h, err := FindWindow("Shell_TrayWnd", ""); err == nil && h != 0 {
		showOne(h)
	}
	EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
		if cName, err := GetClassName(hwnd); err == nil && cName == "Shell_SecondaryTrayWnd" {
			showOne(hwnd)
		}
		return 1
	}, 0)
}

// ForceRestoreTaskbar is the last line of defence: it restores the taskbar and forces
// the whole work area back, without needing the daemon or trusting any cached state.
//
// This is what runs in the recovery path, so it does everything rather than the minimum:
//   - re-shows the taskbar, found from scratch rather than a cached handle
//   - clears a stale AppBar reservation left behind by a process that was killed
//     without running its cleanup
//   - puts the taskbar back at the bottom of its monitor, because the shell will
//     otherwise leave it below the bottom edge of the screen where nobody can see it
//   - writes the work area back to the value Windows ships with, and persists it
//   - nudges the shell into a layout pass and broadcasts the change to applications
//
// Callers are the watchdog and `shellctl --rescue`. Running the taskbar again is
// harmless when it was never hidden, so it is unconditional: restoring a desktop that
// did not need it costs nothing, and failing to restore one that did is unrecoverable
// without a reboot.
func ForceRestoreTaskbar() {
	if err := restoreTaskbars(); err != nil {
		log.Printf("[NovaBar Taskbar] WARNING: the desktop was not fully restored: %v\n", err)
	}

	RefreshWorkArea()

	// Finally repaint. Recovery from a force-kill can leave the destroyed bar's pixels
	// on screen: the window is gone and the work area is correct, but nothing has asked
	// the compositor to replace that region, so the user is left looking at a bar that
	// no longer exists.
	RefreshDesktop()

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
