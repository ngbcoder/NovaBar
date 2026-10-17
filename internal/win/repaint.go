package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procRedrawWindow = modUser32.NewProc("RedrawWindow")

// RedrawWindow flags.
const (
	rdwInvalidate      = 0x0001
	rdwErase           = 0x0004
	rdwAllChildren     = 0x0080
	rdwUpdateNow       = 0x0100
	rdwFrame           = 0x0400
	rdwNoInternalPaint = 0x0002
)

// RefreshDesktop forces the whole desktop to repaint immediately.
//
// Needed on the recovery path for a specific reason. If the daemon is killed while a
// bar window is on screen, the pixels it drew can survive: the window is destroyed by
// the OS, but nothing necessarily asks the compositor to replace that region, so a
// ghost of the bar stays visible on a desktop that is otherwise completely healthy.
// Measured state looks perfect while the user is still looking at a bar that is not
// there any more.
//
// RDW_ERASE plus RDW_FRAME is what makes this effective on DWM-composited desktops;
// RedrawWindow alone leaves the stale frame intact.
func RefreshDesktop() {
	flags := uint32(rdwInvalidate | rdwErase | rdwAllChildren | rdwUpdateNow | rdwFrame)
	_, _, _ = procRedrawWindow.Call(
		0, // NULL: the whole desktop
		0,
		0,
		uintptr(flags),
	)
}

// RefreshDesktopAfterLayout nudges the shell so the compositor recomposites, then
// repaints. Slightly heavier than RefreshDesktop and used when a bar has just been
// created or destroyed, because the change is in the shell's own window layout rather
// than in one region of the screen.
func RefreshDesktopAfterLayout() {
	desktop, _, _ := procGetDesktopWindow.Call()
	_, _, _ = procRedrawWindow.Call(
		desktop,
		0,
		0,
		uintptr(rdwInvalidate|rdwErase|rdwAllChildren|rdwUpdateNow|rdwFrame),
	)
}

// procGetDesktopWindow resolves GetDesktopWindow, only needed by the layout refresh.
var procGetDesktopWindow = modUser32.NewProc("GetDesktopWindow")

// WindowRectFor is a convenience for callers that need a native rect as a struct.
func WindowRectFor(hwnd windows.HWND) (RECT, bool) {
	r, err := GetWindowRect(hwnd)
	if err != nil {
		return r, false
	}
	return r, true
}

var _ unsafe.Pointer
