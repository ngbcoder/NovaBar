package win

import (
	"errors"
	"fmt"
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SystemParametersInfo flags, used when claiming or releasing the work area.
const (
	// SPIF_UPDATEINIFILE persists the change to the user profile.
	SPIF_UPDATEINIFILE = 0x0001
	// SPIF_SENDCHANGE broadcasts WM_SETTINGCHANGE so applications react.
	SPIF_SENDCHANGE = 0x0002
	// SPI_SETWORKAREA
	SPI_SETWORKAREA = 0x002F
)

var procSystemParametersInfoW = modUser32.NewProc("SystemParametersInfoW")

// SetWorkArea writes the desktop work area.
//
// The persist flag controls whether the value is written to the user profile as well.
// Persisting is right for "the whole screen", which is the ordinary state of a working
// desktop, and wrong for a reduced work area, which would shrink the desktop for the
// user after the shell exited.
//
// Returns an error rather than logging: callers in the recovery path need to know.
func SetWorkArea(rect RECT, persist bool) error {
	flags := uintptr(SPIF_SENDCHANGE)
	if persist {
		flags |= SPIF_UPDATEINIFILE
	}

	r1, _, err := procSystemParametersInfoW.Call(
		uintptr(SPI_SETWORKAREA),
		0,
		uintptr(unsafe.Pointer(&rect)),
		flags,
	)
	if r1 == 0 {
		// err is often a stale errno on success paths for this call; report the
		// boolean and keep the message generic so callers are not misled.
		return fmt.Errorf("SystemParametersInfo(SPI_SETWORKAREA) failed (r1=0, lastErr=%v)", err)
	}
	return nil
}

// SetFullWorkArea writes the whole screen as the work area.
//
// The persist flag matters more here than it looks. Persisting "the whole screen"
// writes "there is no taskbar" into the user profile, and the shell then places the
// taskbar below the bottom edge of the screen on every login, until something puts
// the profile back. Hiding the taskbar is a live state and should not outlive the
// process that hid it.
//
// Returns an error rather than logging: callers in the recovery path need to know.
func SetFullWorkArea(persist bool) error {
	screenW := GetSystemMetrics(0) // SM_CXSCREEN
	screenH := GetSystemMetrics(1) // SM_CYSCREEN
	if screenW <= 0 || screenH <= 0 {
		return errors.New("screen metrics unavailable; cannot reset the work area")
	}

	full := RECT{Left: 0, Top: 0, Right: screenW, Bottom: screenH}
	return SetWorkArea(full, persist)
}

// RestoreFullWorkArea forces the desktop work area back to the entire primary display.
//
// This is the blunt instrument recovery needs, and it exists because the polite ones
// are not sufficient. Windows refuses to let an AppBar overlap the real taskbar, and a
// process killed without running its cleanup can leave the shell holding a work-area
// reservation whose window no longer exists. Nothing else will clear that: nudging the
// taskbar only triggers a relayout, which recomputes around the stale reservation
// rather than discarding it.
//
// It is a half-measure on its own. A full-screen work area tells the shell that no
// taskbar is there, and the shell obliges by placing the taskbar below the bottom
// edge of the screen: a window that exists, that the shell believes is visible, and
// that nobody can see. Use RestoreNormalWorkArea to put the desktop back.
//
// Returns an error rather than logging: callers in the recovery path need to know.
func RestoreFullWorkArea() error {
	return SetFullWorkArea(true)
}

// defaultTaskbarHeight is the height of the Windows 11 taskbar at 100% scaling.
//
// It is only a fallback: the real height is measured from the taskbar window, which
// is better because the height changes with the display scale.
const defaultTaskbarHeight = 48

// NormalWorkArea returns the work area Windows ships with: the screen minus the
// strip the taskbar occupies.
//
// This is the value the desktop has to be put back to. A full-screen work area
// tells the shell that no taskbar is there, and the shell obliges by placing the
// taskbar below the bottom edge of the screen: a window that exists, that the
// shell believes is visible, and that nobody can see.
func NormalWorkArea() (RECT, error) {
	screenW := GetSystemMetrics(0) // SM_CXSCREEN
	screenH := GetSystemMetrics(1) // SM_CYSCREEN
	if screenW <= 0 || screenH <= 0 {
		return RECT{}, errors.New("screen metrics unavailable; cannot compute the work area")
	}

	height := int32(defaultTaskbarHeight)
	if hwnd, err := FindWindow("Shell_TrayWnd", ""); err == nil && hwnd != 0 {
		// The taskbar window is the authority on its own height. It is measured
		// rather than assumed because the height follows the display scale.
		//
		// A window parked off-screen at zero size would report nothing useful, so
		// an implausible measurement is ignored in favour of the default.
		if rect, err := GetWindowRect(hwnd); err == nil && rect.Height() > 8 && rect.Height() < 200 {
			height = rect.Height()
		}
	}

	return RECT{Left: 0, Top: 0, Right: screenW, Bottom: screenH - height}, nil
}

// RestoreNormalWorkArea puts the work area back the way Windows ships it: the screen
// minus the strip the taskbar occupies.
//
// It is persisted, because the point of the recovery path is that the desktop comes
// back, including after a reboot.
func RestoreNormalWorkArea() error {
	normal, err := NormalWorkArea()
	if err != nil {
		return err
	}
	return SetWorkArea(normal, true)
}

// RefreshWorkArea forces Windows to recompute the desktop work area and tells every
// top-level window about it.
//
// This is not optional housekeeping. The shell caches the work area when it lays out
// the taskbar, and ShowWindow(SW_HIDE) on the taskbar does not invalidate that cache.
// The consequence is concrete: after hiding the taskbar the bottom strip stays excluded,
// so a bottom AppBar asking for it is refused. Without this call, a bottom bar is
// invisible no matter what the config says.
//
// Two mechanisms, because neither alone is reliable:
//   - resizing the taskbar windows forces the shell's layout pass to run again
//   - broadcasting WM_SETTINGCHANGE makes applications react to the new work area,
//     including the ones we have already tiled
func RefreshWorkArea() {
	nudgeTaskbars()
	broadcastWorkAreaChange()
}

// nudgeTaskbars resizes each taskbar window to the same size it already has.
//
// Moving a taskbar by zero pixels would be ignored, so the width is nudged by one and
// restored immediately. The shell treats any taskbar geometry change as a request to
// redo the layout, which is what recomputes the work area.
func nudgeTaskbars() {
	var toRestore []struct {
		hwnd   windows.HWND
		rect   RECT
		exists bool
	}

	EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
		class, err := GetClassName(hwnd)
		if err != nil || (class != "Shell_TrayWnd" && class != "Shell_SecondaryTrayWnd") {
			return 1
		}

		rect, err := GetWindowRect(hwnd)
		if err != nil || rect.Width() <= 0 || rect.Height() <= 0 {
			return 1
		}

		entry := struct {
			hwnd   windows.HWND
			rect   RECT
			exists bool
		}{hwnd: hwnd, rect: rect}

		_ = SetWindowPos(hwnd, 0,
			rect.Left, rect.Top,
			rect.Width()+1, rect.Height(),
			SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
		toRestore = append(toRestore, entry)
		return 1
	}, 0)

	// Put them back, so the visible shell is not left one pixel wider.
	for _, entry := range toRestore {
		_ = SetWindowPos(entry.hwnd, 0,
			entry.rect.Left, entry.rect.Top,
			entry.rect.Width(), entry.rect.Height(),
			SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
	}
}

// broadcastWorkAreaChange tells every window that the work area moved.
func broadcastWorkAreaChange() {
	// A short timeout: one hung full-screen app must not stall shell startup.
	const timeoutMS = 2000

	r1, _, _ := procSendMessageTimeoutW.Call(
		uintptr(HWND_BROADCAST),
		uintptr(WM_SETTINGCHANGE),
		0,
		uintptr(SPI_SETWORKAREA),
		uintptr(SMTO_ABORTIFHUNG),
		uintptr(timeoutMS),
		0,
	)
	if r1 == 0 {
		// Not fatal. The work area itself is already correct by this point; this only
		// affects how promptly applications react.
		log.Printf("[NovaBar] Could not broadcast the work-area change (SendMessageTimeout returned 0)\n")
	}
}

// WorkAreaAfterInsets returns the work area Windows reports once the shell has
// reprocessed the taskbar layout.
//
// Used as a settle step: nudging the taskbar is asynchronous with respect to the work
// area becoming queryable, so callers that need the reclaimed space immediately poll
// this briefly.
func WorkAreaAfterInsets(before Height) (Height, bool) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		after, err := PrimaryWorkArea()
		if err == nil && after != before {
			return after, true
		}
		if time.Now().After(deadline) {
			return after, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// workAreaSettleTimeout is how long the work area is watched for further changes
// before it is considered settled.
const workAreaSettleTimeout = 300 * time.Millisecond

// WaitForWorkAreaSettle waits until the work area stops changing.
//
// The shell reacts to a work-area change asynchronously, and its reaction includes
// repositioning the taskbar. A caller that writes the work area and then puts the
// taskbar back can have the shell's late reaction undo the work, which is how a
// rescue ends with the taskbar parked off-screen: the shell had the last word, and
// its word was "there is no room for a taskbar".
func WaitForWorkAreaSettle() {
	previous, err := PrimaryWorkArea()
	if err != nil {
		return
	}

	deadline := time.Now().Add(workAreaSettleTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)

		current, err := PrimaryWorkArea()
		if err != nil {
			return
		}
		if current != previous {
			// It moved, so it is not settled yet. The clock starts again.
			previous = current
			deadline = time.Now().Add(workAreaSettleTimeout)
		}
	}
}

// Height is a screen-space vertical measurement used by WorkAreaAfterInsets.
type Height = int32

// PrimaryWorkArea returns the current work area of the primary display as
// (height, top).
func PrimaryWorkArea() (int32, error) {
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))

	hMon, _, _ := procMonitorFromWindow.Call(0, uintptr(MONITOR_DEFAULTTOPRIMARY))
	if hMon == 0 {
		return 0, errNoMonitor
	}
	if !GetMonitorInfo(hMon, &mi) {
		return 0, errNoMonitor
	}
	return mi.RcWork.Height(), nil
}

// PrimaryWorkAreaRect returns the work area of the primary display as a rectangle.
//
// The rectangle is what the shell currently believes is usable, AppBar reservations
// included, which makes it the authority on whether a bar still owns its space.
func PrimaryWorkAreaRect() (RECT, error) {
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))

	hMon, _, _ := procMonitorFromWindow.Call(0, uintptr(MONITOR_DEFAULTTOPRIMARY))
	if hMon == 0 {
		return RECT{}, errNoMonitor
	}
	if !GetMonitorInfo(hMon, &mi) {
		return RECT{}, errNoMonitor
	}
	return mi.RcWork, nil
}

// errNoMonitor is returned when the primary monitor cannot be resolved.
var errNoMonitor = errors.New("could not resolve the primary monitor")

// procSendMessageTimeoutW resolves SendMessageTimeoutW lazily; it is only needed on the
// work-area path and pulling it into the main proc block would be noise.
var procSendMessageTimeoutW = modUser32.NewProc("SendMessageTimeoutW")
