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
	modNtdll    = windows.NewLazySystemDLL("ntdll.dll")

	// User32 procedures
	procSetWinEventHook          = modUser32.NewProc("SetWinEventHook")
	procUnhookWinEvent           = modUser32.NewProc("UnhookWinEvent")
	procGetMessageW              = modUser32.NewProc("GetMessageW")
	procTranslateMessage         = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW         = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage          = modUser32.NewProc("PostQuitMessage")
	procPostMessageW             = modUser32.NewProc("PostMessageW")
	procFindWindowW              = modUser32.NewProc("FindWindowW")
	procShowWindow               = modUser32.NewProc("ShowWindow")
	procIsWindowVisible          = modUser32.NewProc("IsWindowVisible")
	procGetWindowTextW           = modUser32.NewProc("GetWindowTextW")
	procGetClassNameW            = modUser32.NewProc("GetClassNameW")
	procGetWindowLongPtrW        = modUser32.NewProc("GetWindowLongPtrW")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procEnumWindows              = modUser32.NewProc("EnumWindows")
	procSetWindowPos             = modUser32.NewProc("SetWindowPos")
	procBeginDeferWindowPos      = modUser32.NewProc("BeginDeferWindowPos")
	procDeferWindowPos           = modUser32.NewProc("DeferWindowPos")
	procEndDeferWindowPos        = modUser32.NewProc("EndDeferWindowPos")
	procRegisterHotKey           = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = modUser32.NewProc("UnregisterHotKey")
	procGetSystemMetrics         = modUser32.NewProc("GetSystemMetrics")

	// Dwmapi procedures
	procDwmGetWindowAttribute = modDwmapi.NewProc("DwmGetWindowAttribute")

	// Shell32 procedures
	procSHAppBarMessage = modShell32.NewProc("SHAppBarMessage")

	// Monitor procedures
	procEnumDisplayMonitors = modUser32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = modUser32.NewProc("GetMonitorInfoW")
	procMonitorFromWindow   = modUser32.NewProc("MonitorFromWindow")

	// Window class and creation procedures
	procRegisterClassExW = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW   = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow    = modUser32.NewProc("DestroyWindow")

	// Window geometry / activation
	procGetWindowRectW      = modUser32.NewProc("GetWindowRect")
	procGetForegroundWindow = modUser32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procIsIconic            = modUser32.NewProc("IsIconic")
	procIsWindow            = modUser32.NewProc("IsWindow")
	procMoveWindow          = modUser32.NewProc("MoveWindow")
	procPostThreadMessageW  = modUser32.NewProc("PostThreadMessageW")
)

type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

const (
	MONITOR_DEFAULTTONULL    = 0x00000000
	MONITOR_DEFAULTTOPRIMARY = 0x00000001
	MONITOR_DEFAULTTONEAREST = 0x00000002
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

	OBJID_WINDOW   = 0x00000000
	INDEXID_OBJECT = 0
)

// Window messages handled on the pinned message pump thread.
const (
	WM_HOTKEY  = 0x0312
	WM_CLOSE   = 0x0010
	WM_DESTROY = 0x0002
)

// Window styles and attributes
const (
	GWL_STYLE        = -16
	GWL_EXSTYLE      = -20
	WS_POPUP         = 0x80000000
	WS_VISIBLE       = 0x10000000
	WS_EX_TOPMOST    = 0x00000008
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

	// WM_SETTINGCHANGE, used to announce a work-area change.
	WM_SETTINGCHANGE = 0x001A

	// SMTO_ABORTIFHUNG
	SMTO_ABORTIFHUNG = 0x0002

	HWND_BROADCAST = 0xffff

	HWND_TOP       = 0
	HWND_BOTTOM    = 1
	HWND_TOPMOST   = ^uintptr(0) // -1
	HWND_NOTOPMOST = ^uintptr(1) // -2

	// DWM Window Attributes
	DWMWA_EXTENDED_FRAME_BOUNDS = 9
	DWMWA_CLOAKED               = 14
	DWM_CLOAKED_APP             = 0x00000001
	DWM_CLOAKED_SHELL           = 0x00000002
	DWM_CLOAKED_INHERITED       = 0x00000004

	// AppBar messages
	ABM_NEW              = 0x00000000
	ABM_REMOVE           = 0x00000001
	ABM_QUERYPOS         = 0x00000002
	ABM_SETPOS           = 0x00000003
	ABM_GETSTATE         = 0x00000004
	ABM_GETTASKBARPOS    = 0x00000005
	ABM_ACTIVATE         = 0x00000006
	ABM_GETAUTOHIDEBAR   = 0x00000007
	ABM_SETAUTOHIDEBAR   = 0x00000008
	ABM_WINDOWPOSCHANGED = 0x00000009
	ABM_SETSTATE         = 0x0000000A

	// AppBar states, used with ABM_SETSTATE.
	ABS_AUTOHIDE    = 0x00000001
	ABS_AUTOHIDEBAR = 0x00000002
	ABS_ALWAYSONTOP = 0x00000004

	ABE_LEFT   = 0
	ABE_TOP    = 1
	ABE_RIGHT  = 2
	ABE_BOTTOM = 3
)

// Hotkey modifiers
const (
	MOD_ALT      = 0x0001
	MOD_CONTROL  = 0x0002
	MOD_SHIFT    = 0x0004
	MOD_WIN      = 0x0008
	MOD_NOREPEAT = 0x4000
)

// Virtual-key codes used by the default hotkey bindings.
const (
	VK_R     = 0x52
	VK_SPACE = 0x20
	VK_LEFT  = 0x25
	VK_RIGHT = 0x27
	VK_1     = 0x31
	VK_2     = 0x32
	VK_3     = 0x33
	VK_4     = 0x34
	VK_5     = 0x35
	VK_6     = 0x36
	VK_7     = 0x37
	VK_8     = 0x38
	VK_9     = 0x39
)

// HotkeyID identifies a registered hotkey in WM_HOTKEY messages. Ids are unique
// within the daemon and dispatched by internal/hotkey.
const (
	HotkeyRescue uintptr = iota + 1
	HotkeyLauncher
	HotkeyWorkspacePrev
	HotkeyWorkspaceNext
)

// RECT carries explicit JSON tags so widgets see the same field names as every other
// payload crossing the IPC boundary.
type RECT struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
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

// PostMessage posts a message to a window's queue without waiting for it to be
// processed.
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

// PostThreadMessage posts a message to a specific thread's message queue.
//
// Used to hand work to the message pump from another goroutine: PostQuitMessage and
// RegisterHotKey are both thread-affine, so anything crossing a goroutine boundary
// has to arrive as a message instead.
func PostThreadMessage(threadID uint32, msg uint32, wParam, lParam uintptr) error {
	r1, _, err := procPostThreadMessageW.Call(
		uintptr(threadID),
		uintptr(msg),
		wParam,
		lParam,
	)
	if r1 == 0 {
		return err
	}
	return nil
}

// FindWindow returns the top-level window matching the given class and/or title.
// Pass "" for either to match on the other alone.
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

func GetMonitorInfo(hMonitor uintptr, lpmi *MONITORINFO) bool {
	lpmi.CbSize = uint32(unsafe.Sizeof(*lpmi))
	r1, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(lpmi)))
	return r1 != 0
}

func MonitorFromWindow(hwnd windows.HWND, dwFlags uint32) uintptr {
	r1, _, _ := procMonitorFromWindow.Call(uintptr(hwnd), uintptr(dwFlags))
	return r1
}

func BeginDeferWindowPos(nNumWindows int32) uintptr {
	r1, _, _ := procBeginDeferWindowPos.Call(uintptr(nNumWindows))
	return r1
}

func DeferWindowPos(hWinPosInfo uintptr, hwnd windows.HWND, hWndInsertAfter uintptr, x, y, cx, cy int32, uFlags uint32) uintptr {
	r1, _, _ := procDeferWindowPos.Call(
		hWinPosInfo,
		uintptr(hwnd),
		hWndInsertAfter,
		uintptr(x),
		uintptr(y),
		uintptr(cx),
		uintptr(cy),
		uintptr(uFlags),
	)
	return r1
}

func EndDeferWindowPos(hWinPosInfo uintptr) bool {
	r1, _, _ := procEndDeferWindowPos.Call(hWinPosInfo)
	return r1 != 0
}

func RegisterClassEx(wcx *WNDCLASSEX) (uint16, error) {
	wcx.CbSize = uint32(unsafe.Sizeof(*wcx))
	r1, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(wcx)))
	if r1 == 0 {
		return 0, err
	}
	return uint16(r1), nil
}

func CreateWindowEx(dwExStyle uint32, lpClassName, lpWindowName *uint16, dwStyle uint32, x, y, nWidth, nHeight int32, hWndParent windows.HWND, hMenu windows.Handle, hInstance windows.Handle, lpParam unsafe.Pointer) (windows.HWND, error) {
	r1, _, err := procCreateWindowExW.Call(
		uintptr(dwExStyle),
		uintptr(unsafe.Pointer(lpClassName)),
		uintptr(unsafe.Pointer(lpWindowName)),
		uintptr(dwStyle),
		uintptr(x),
		uintptr(y),
		uintptr(nWidth),
		uintptr(nHeight),
		uintptr(hWndParent),
		uintptr(hMenu),
		uintptr(hInstance),
		uintptr(lpParam),
	)
	if r1 == 0 {
		return 0, err
	}
	return windows.HWND(r1), nil
}

func DefWindowProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	r1, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r1
}

func DestroyWindow(hwnd windows.HWND) bool {
	r1, _, _ := procDestroyWindow.Call(uintptr(hwnd))
	return r1 != 0
}

func GetSystemMetrics(nIndex int32) int32 {
	r1, _, _ := procGetSystemMetrics.Call(uintptr(nIndex))
	return int32(r1)
}

// GetWindowRect returns the window rectangle in screen coordinates. Unlike
// GetExtendedFrameBounds this is the "real" window rect and is what DWM border
// compensation must be measured against.
func GetWindowRect(hwnd windows.HWND) (RECT, error) {
	var r RECT
	r1, _, err := procGetWindowRectW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	if r1 == 0 {
		return r, err
	}
	return r, nil
}

// IsValid reports whether hwnd still refers to a live window. Stale handles from a
// previous enumeration are expected once windows are destroyed.
func IsValid(hwnd windows.HWND) bool {
	r1, _, _ := procIsWindow.Call(uintptr(hwnd))
	return r1 != 0
}

// GetForegroundWindow returns the currently focused top-level window, or 0.
func GetForegroundWindow() windows.HWND {
	r1, _, _ := procGetForegroundWindow.Call()
	return windows.HWND(r1)
}

// SetForegroundWindow brings hwnd to the foreground. Returns false when Windows
// denies the request, which it does for background processes.
func SetForegroundWindow(hwnd windows.HWND) bool {
	r1, _, _ := procSetForegroundWindow.Call(uintptr(hwnd))
	return r1 != 0
}

// IsMinimized reports whether hwnd is currently minimized.
func IsMinimized(hwnd windows.HWND) bool {
	r1, _, _ := procIsIconic.Call(uintptr(hwnd))
	return r1 != 0
}

// RegisterHotKey binds a system-wide hotkey. On success the daemon receives
// WM_HOTKEY with the given id on its message pump thread.
//
// Must be called from the thread running the message loop.
func RegisterHotKey(id uintptr, modifiers uint32, vk uint32) error {
	r1, _, err := procRegisterHotKey.Call(0, id, uintptr(modifiers), uintptr(vk))
	if r1 == 0 {
		return err
	}
	return nil
}

// UnregisterHotKey releases a hotkey previously bound with RegisterHotKey.
func UnregisterHotKey(id uintptr) bool {
	r1, _, _ := procUnregisterHotKey.Call(0, id)
	return r1 != 0
}

// RegisterHotKeyResult describes the outcome of a single hotkey registration.
type RegisterHotKeyResult struct {
	ID   uintptr
	Spec string
	Err  error
}

// RegisterHotKeys binds a batch of hotkeys, reporting per-key outcomes instead of
// failing fast. A key already owned by another application is a common and
// non-fatal condition, so callers surface it and carry on. Results come back in
// ascending id order so logging is deterministic.
func RegisterHotKeys(bindings map[uintptr]HotkeyBinding) []RegisterHotKeyResult {
	results := make([]RegisterHotKeyResult, 0, len(bindings))
	for _, id := range SortedHotkeyIDs(bindings) {
		b := bindings[id]
		err := RegisterHotKey(id, b.Modifiers, b.VK)
		results = append(results, RegisterHotKeyResult{ID: id, Spec: b.String(), Err: err})
	}
	return results
}

// UnregisterHotKeys releases every previously registered hotkey id.
func UnregisterHotKeys(ids []uintptr) {
	for _, id := range ids {
		UnregisterHotKey(id)
	}
}
