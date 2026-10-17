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

// RestoreFullWorkArea forces the desktop work area back to the entire primary display.
//
// This is the blunt instrument recovery needs, and it exists because the polite ones
// are not sufficient. Windows refuses to let an AppBar overlap the real taskbar, and a
// process killed without running its cleanup can leave the shell holding a work-area
// reservation whose window no longer exists. Nothing else will clear that: nudging the
// taskbar only triggers a relayout, which recomputes around the stale reservation
// rather than discarding it.
//
// SPIF_UPDATEINIFILE is deliberate. The value written is "the whole screen", which is
// the ordinary state of a working desktop, so persisting it is harmless and makes the
// recovery survive a reboot. Writing a reduced work area to the profile would be the
// dangerous version of this, which is why it is never done.
//
// Returns an error rather than logging: callers in the recovery path need to know.
func RestoreFullWorkArea() error {
	screenW := GetSystemMetrics(0) // SM_CXSCREEN
	screenH := GetSystemMetrics(1) // SM_CYSCREEN
	if screenW <= 0 || screenH <= 0 {
		return errors.New("screen metrics unavailable; cannot reset the work area")
	}

	var full RECT
	full.Left = 0
	full.Top = 0
	full.Right = screenW
	full.Bottom = screenH

	r1, _, err := procSystemParametersInfoW.Call(
		uintptr(SPI_SETWORKAREA),
		0,
		uintptr(unsafe.Pointer(&full)),
		uintptr(SPIF_UPDATEINIFILE|SPIF_SENDCHANGE),
	)
	if r1 == 0 {
		// err is often a stale errno on success paths for this call; report the
		// boolean and keep the message generic so callers are not misled.
		return fmt.Errorf("SystemParametersInfo(SPI_SETWORKAREA) failed (r1=0, lastErr=%v)", err)
	}
	return nil
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

	SetEnumWindowsHandler(func(hwnd windows.HWND, lParam uintptr) uintptr {
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
	})
	EnumWindows(0)
	ClearEnumWindowsHandler()

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

// errNoMonitor is returned when the primary monitor cannot be resolved.
var errNoMonitor = errors.New("could not resolve the primary monitor")

// procSendMessageTimeoutW resolves SendMessageTimeoutW lazily; it is only needed on the
// work-area path and pulling it into the main proc block would be noise.
var procSendMessageTimeoutW = modUser32.NewProc("SendMessageTimeoutW")
