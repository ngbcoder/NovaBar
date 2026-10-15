package win

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modDwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	// User32 procedures
	procSetWinEventHook      = modUser32.NewProc("SetWinEventHook")
	procUnhookWinEvent        = modUser32.NewProc("UnhookWinEvent")
	procGetMessageW          = modUser32.NewProc("GetMessageW")
	procTranslateMessage     = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW     = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage      = modUser32.NewProc("PostQuitMessage")
	procPostMessageW         = modUser32.NewProc("PostMessageW")
	procFindWindowW          = modUser32.NewProc("FindWindowW")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowTextW       = modUser32.NewProc("GetWindowTextW")
	procGetClassNameW        = modUser32.NewProc("GetClassNameW")
	procGetWindowLongPtrW    = modUser32.NewProc("GetWindowLongPtrW")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procBeginDeferWindowPos  = modUser32.NewProc("BeginDeferWindowPos")
	procDeferWindowPos       = modUser32.NewProc("DeferWindowPos")
	procEndDeferWindowPos    = modUser32.NewProc("EndDeferWindowPos")
	procRegisterHotKey       = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey     = modUser32.NewProc("UnregisterHotKey")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")

	// Dwmapi procedures
	procDwmGetWindowAttribute = modDwmapi.NewProc("DwmGetWindowAttribute")

	// Shell32 procedures
	procSHAppBarMessage = modShell32.NewProc("SHAppBarMessage")
)

// WinEvent constants
const (
	WINEVENT_OUTOFCONTEXT   = 0x0000
	WINEVENT_SKIPOWNPROCESS = 0x0002

	EVENT_SYSTEM_FOREGROUND    = 0x0003
	EVENT_SYSTEM_MOVESIZESTART = 0x000A
	EVENT_SYSTEM_MOVESIZEEND   = 0x000B
	EVENT_SYSTEM_MINIMIZESTART = 0x0016
	EVENT_SYSTEM_MINIMIZEEND   = 0x0017
	EVENT_OBJECT_CREATE        = 0x8000
	EVENT_OBJECT_DESTROY       = 0x8001
	EVENT_OBJECT_SHOW          = 0x8002
	EVENT_OBJECT_HIDE          = 0x8003
	EVENT_OBJECT_NAMECHANGE    = 0x800C

	OBJID_WINDOW = 0x00000000
	INDEXID_OBJECT = 0
)

// Window styles and attributes
const (
	GWL_STYLE     = -16
	GWL_EXSTYLE   = -20
	WS_VISIBLE    = 0x10000000
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_APPWINDOW  = 0x00040000
	WS_EX_NOACTIVATE = 0x08000000

	SW_HIDE            = 0
	SW_SHOWNORMAL      = 1
	SW_SHOWMINIMIZED   = 2
	SW_SHOWMAXIMIZED   = 3
	SW_SHOWNOACTIVATE  = 4
	SW_SHOW            = 5
	SW_MINIMIZE        = 6
	SW_SHOWMINNOACTIVE = 7
	SW_SHOWNA          = 8
	SW_RESTORE         = 9

	SWP_NOSIZE         = 0x0001
	SWP_NOMOVE         = 0x0002
	SWP_NOZORDER       = 0x0004
	SWP_NOREDRAW       = 0x0008
	SWP_NOACTIVATE     = 0x0010
	SWP_FRAMECHANGED   = 0x0020
	SWP_SHOWWINDOW     = 0x0040
	SWP_HIDEWINDOW     = 0x0080
	SWP_NOOWNERZORDER  = 0x0200
	SWP_NOSENDCHANGING = 0x0400

	HWND_TOP    = 0
	HWND_BOTTOM = 1
	HWND_TOPMOST = ^uintptr(0) // -1
	HWND_NOTOPMOST = ^uintptr(1) // -2

	// DWM Window Attributes
	DWMWA_EXTENDED_FRAME_BOUNDS = 9
	DWMWA_CLOAKED               = 14
	DWM_CLOAKED_APP             = 0x00000001
	DWM_CLOAKED_SHELL           = 0x00000002
	DWM_CLOAKED_INHERITED       = 0x00000004

	// AppBar messages
	ABM_NEW        = 0x00000000
	ABM_REMOVE     = 0x00000001
	ABM_QUERYPOS   = 0x00000002
	ABM_SETPOS     = 0x00000003
	ABM_GETSTATE   = 0x00000004
	ABM_GETTASKBARPOS = 0x00000005
	ABM_ACTIVATE   = 0x00000006
	ABM_GETAUTOHIDEBAR = 0x00000007
	ABM_SETAUTOHIDEBAR = 0x00000008
	ABM_WINDOWPOSCHANGED = 0x00000009
	ABM_SETSTATE   = 0x0000000A

	ABE_LEFT   = 0
	ABE_TOP    = 1
	ABE_RIGHT  = 2
	ABE_BOTTOM = 3
)

type RECT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r RECT) Width() int32  { return r.Right - r.Left }
func (r RECT) Height() int32 { return r.Bottom - r.Top }

type POINT struct {
	X int32
	Y int32
}

type MSG struct {
	HWnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type APPBARDATA struct {
	CbSize           uint32
	HWnd             windows.HWND
	UCallbackMessage uint32
	UEdge            uint32
	Rc               RECT
	LParam           uintptr
}

// WinEventProc is the signature for SetWinEventHook callbacks.
type WinEventProc func(hWinEventHook uintptr, event uint32, hwnd windows.HWND, idObject int32, idChild int32, idEventThread uint32, dwmsEventTime uint32) uintptr

func SetWinEventHook(eventMin, eventMax uint32, hmodWinEvent uintptr, fn uintptr, idProcess, idThread, dwFlags uint32) (uintptr, error) {
	r1, _, err := procSetWinEventHook.Call(
		uintptr(eventMin),
		uintptr(eventMax),
		hmodWinEvent,
		fn,
		uintptr(idProcess),
		uintptr(idThread),
		uintptr(dwFlags),
	)
	if r1 == 0 {
		return 0, err
	}
	return r1, nil
}

func UnhookWinEvent(hWinEventHook uintptr) bool {
	r1, _, _ := procUnhookWinEvent.Call(hWinEventHook)
	return r1 != 0
}

func GetMessage(msg *MSG, hwnd windows.HWND, msgFilterMin, msgFilterMax uint32) (int32, error) {
	r1, _, err := procGetMessageW.Call(
		uintptr(unsafe.Pointer(msg)),
		uintptr(hwnd),
		uintptr(msgFilterMin),
		uintptr(msgFilterMax),
	)
	if int32(r1) == -1 {
		return -1, err
	}
	return int32(r1), nil
}

func TranslateMessage(msg *MSG) bool {
	r1, _, _ := procTranslateMessage.Call(uintptr(unsafe.Pointer(msg)))
	return r1 != 0
}

func DispatchMessage(msg *MSG) uintptr {
	r1, _, _ := procDispatchMessageW.Call(uintptr(unsafe.Pointer(msg)))
	return r1
}

func PostQuitMessage(exitCode int32) {
	procPostQuitMessage.Call(uintptr(exitCode))
}

func PostMessage(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) error {
	r1, _, err := procPostMessageW.Call(
		uintptr(hwnd),
		uintptr(msg),
		wParam,
		lParam,
	)
	if r1 == 0 {
		return err
	}
	return nil
}

func FindWindow(className, windowName string) (windows.HWND, error) {
	var cNamePtr, wNamePtr *uint16
	var err error
	if className != "" {
		cNamePtr, err = windows.UTF16PtrFromString(className)
		if err != nil {
			return 0, err
		}
	}
	if windowName != "" {
		wNamePtr, err = windows.UTF16PtrFromString(windowName)
		if err != nil {
			return 0, err
		}
	}
	r1, _, err := procFindWindowW.Call(uintptr(unsafe.Pointer(cNamePtr)), uintptr(unsafe.Pointer(wNamePtr)))
	if r1 == 0 {
		return 0, err
	}
	return windows.HWND(r1), nil
}

func ShowWindow(hwnd windows.HWND, cmdShow int32) bool {
	r1, _, _ := procShowWindow.Call(uintptr(hwnd), uintptr(cmdShow))
	return r1 != 0
}

func IsWindowVisible(hwnd windows.HWND) bool {
	r1, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return r1 != 0
}

func GetWindowText(hwnd windows.HWND) (string, error) {
	buf := make([]uint16, 512)
	r1, _, err := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r1 == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf[:r1]), nil
}

func GetClassName(hwnd windows.HWND) (string, error) {
	buf := make([]uint16, 256)
	r1, _, err := procGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r1 == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf[:r1]), nil
}

func GetWindowLongPtr(hwnd windows.HWND, index int) (uintptr, error) {
	r1, _, err := procGetWindowLongPtrW.Call(uintptr(hwnd), uintptr(index))
	if r1 == 0 {
		return 0, err
	}
	return r1, nil
}

func GetWindowPID(hwnd windows.HWND) (uint32, error) {
	var pid uint32
	r1, _, err := procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	if r1 == 0 {
		return 0, err
	}
	return pid, nil
}

func EnumWindows(callback uintptr, lParam uintptr) error {
	r1, _, err := procEnumWindows.Call(callback, lParam)
	if r1 == 0 {
		return err
	}
	return nil
}

func SetWindowPos(hwnd windows.HWND, hWndInsertAfter uintptr, x, y, cx, cy int32, uFlags uint32) error {
	r1, _, err := procSetWindowPos.Call(
		uintptr(hwnd),
		hWndInsertAfter,
		uintptr(x),
		uintptr(y),
		uintptr(cx),
		uintptr(cy),
		uintptr(uFlags),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

func DwmGetWindowAttribute(hwnd windows.HWND, dwAttribute uint32, pvAttribute unsafe.Pointer, cbAttribute uint32) error {
	r1, _, _ := procDwmGetWindowAttribute.Call(
		uintptr(hwnd),
		uintptr(dwAttribute),
		uintptr(pvAttribute),
		uintptr(cbAttribute),
	)
	if r1 != 0 {
		return syscall.Errno(r1)
	}
	return nil
}

func IsWindowCloaked(hwnd windows.HWND) bool {
	var cloaked uint32
	err := DwmGetWindowAttribute(hwnd, DWMWA_CLOAKED, unsafe.Pointer(&cloaked), uint32(unsafe.Sizeof(cloaked)))
	if err != nil {
		return false
	}
	return cloaked != 0
}

func GetExtendedFrameBounds(hwnd windows.HWND) (RECT, error) {
	var r RECT
	err := DwmGetWindowAttribute(hwnd, DWMWA_EXTENDED_FRAME_BOUNDS, unsafe.Pointer(&r), uint32(unsafe.Sizeof(r)))
	return r, err
}

func SHAppBarMessage(dwMessage uint32, pData *APPBARDATA) uintptr {
	r1, _, _ := procSHAppBarMessage.Call(uintptr(dwMessage), uintptr(unsafe.Pointer(pData)))
	return r1
}
