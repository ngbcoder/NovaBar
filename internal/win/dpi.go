package win

import (
	"golang.org/x/sys/windows"
)

var (
	modUser32Dpi                      = windows.NewLazySystemDLL("user32.dll")
	procSetProcessDpiAwarenessContext = modUser32Dpi.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = modUser32Dpi.NewProc("SetProcessDPIAware")

	modShcore                  = windows.NewLazySystemDLL("shcore.dll")
	procSetProcessDpiAwareness = modShcore.NewProc("SetProcessDpiAwareness")
)

const (
	DpiAwarenessContextPerMonitorAwareV2 = uintptr(0xfffffffc) // -4
	ProcessPerMonitorDpiAware            = 2
)

// EnablePerMonitorDpiAwareness enables per-monitor DPI awareness V2 for the process
// so windows, appbars, and WebView2 surfaces render crisply on mixed-DPI displays.
func EnablePerMonitorDpiAwareness() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		_, _, _ = procSetProcessDpiAwarenessContext.Call(DpiAwarenessContextPerMonitorAwareV2)
		return
	}
	if procSetProcessDpiAwareness.Find() == nil {
		_, _, _ = procSetProcessDpiAwareness.Call(uintptr(ProcessPerMonitorDpiAware))
		return
	}
	if procSetProcessDPIAware.Find() == nil {
		_, _, _ = procSetProcessDPIAware.Call()
		return
	}
}
