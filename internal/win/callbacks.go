package win

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windows.NewCallback allocates an unmanaged Go callback thunk. It must be called
// exactly once per distinct function shape and the result reused; calling it inside
// a hot path (a window enumeration, an event handler) leaks a thunk on every
// invocation.
//
// Every callback thunk in the daemon lives in this file so that the total number is
// fixed and auditable. Each is a package-level var, created once at init.

// enumWindowsCallback is the shared EnumWindows trampoline. It forwards to the
// active handler installed by SetEnumWindowsHandler.
var enumWindowsCallback = windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
	enumMu.RLock()
	h := enumHandler
	enumMu.RUnlock()
	if h == nil {
		return 1
	}
	return h(hwnd, lParam)
})

// EnumProc is the signature of an EnumWindows visitor.
type EnumProc func(hwnd windows.HWND, lParam uintptr) uintptr

var (
	enumMu      sync.RWMutex
	enumHandler EnumProc
	enumDepth   int

	// enumWalkMu serializes whole walks.
	enumWalkMu sync.Mutex
)

// EnumWindowsWith enumerates top-level windows, invoking proc for each.
//
// The visitor is passed in rather than installed globally, and the walk is
// serialized against every other walk.
//
// This matters because the trampoline is a single fixed thunk with one handler
// slot. Two walks running at once would invoke each other's visitors: one would
// see the other's windows and the other would see none. That is not a crash, so it
// goes unnoticed, but it loses windows — and it loses them silently, which is how a
// guard that re-hides the taskbar ends up doing nothing.
//
// Returns true when at least one window was visited.
func EnumWindowsWith(proc EnumProc, lParam uintptr) bool {
	enumWalkMu.Lock()
	defer enumWalkMu.Unlock()

	enumMu.Lock()
	enumHandler = proc
	enumDepth++
	enumMu.Unlock()

	r1, _, _ := procEnumWindows.Call(enumWindowsCallback, lParam)

	enumMu.Lock()
	enumDepth = 0
	enumHandler = nil
	enumMu.Unlock()

	return r1 != 0
}

// enumMonitorsCallback is the shared EnumDisplayMonitors trampoline.
var enumMonitorsCallback = windows.NewCallback(func(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr {
	monitorMu.RLock()
	h := monitorHandler
	monitorMu.RUnlock()
	if h == nil {
		return 1
	}
	return h(hMonitor, hdc, lprc, dwData)
})

// MonitorProc is the signature of an EnumDisplayMonitors visitor.
type MonitorProc func(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr

var (
	monitorMu      sync.RWMutex
	monitorHandler MonitorProc
	monitorDepth   int
)

// SetMonitorHandler installs the handler invoked by the EnumDisplayMonitors
// trampoline. One at a time; calls are serialized.
func SetMonitorHandler(proc MonitorProc) {
	monitorMu.Lock()
	monitorHandler = proc
	monitorMu.Unlock()
}

// EnumDisplayMonitors enumerates displays using the shared trampoline.
func EnumDisplayMonitors(hdc uintptr, lprcClip *RECT) bool {
	monitorMu.Lock()
	if monitorDepth > 0 {
		monitorMu.Unlock()
		return false
	}
	monitorDepth++
	monitorMu.Unlock()

	var clipPtr uintptr
	if lprcClip != nil {
		clipPtr = uintptr(unsafe.Pointer(lprcClip))
	}

	r1, _, _ := procEnumDisplayMonitors.Call(hdc, clipPtr, enumMonitorsCallback, 0)

	monitorMu.Lock()
	monitorDepth = 0
	monitorMu.Unlock()

	return r1 != 0
}
