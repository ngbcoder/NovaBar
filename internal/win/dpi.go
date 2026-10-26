package win

import (
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32Dpi                      = windows.NewLazySystemDLL("user32.dll")
	procSetProcessDpiAwarenessContext = modUser32Dpi.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = modUser32Dpi.NewProc("SetProcessDPIAware")

	modShcore                  = windows.NewLazySystemDLL("shcore.dll")
	procSetProcessDpiAwareness = modShcore.NewProc("SetProcessDpiAwareness")
	procGetDpiForMonitor       = modShcore.NewProc("GetDpiForMonitor")
)

const (
	DpiAwarenessContextPerMonitorAwareV2 = uintptr(0xfffffffc) // -4
	ProcessPerMonitorDpiAware            = 2
	MonitorDefaultToNearest              = 2
)

// MonitorFromRect wraps MonitorFromRect.
func MonitorFromRect(r RECT) uintptr {
	h, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), uintptr(MonitorDefaultToNearest))
	return h
}

// MonitorDpi returns the effective DPI of the monitor containing r (or the
// primary monitor when r is empty). Falls back to 96 on any failure.
func MonitorDpi(r RECT) (uint, uint) {
	hMon := MonitorFromRect(r)
	if hMon == 0 {
		hMon, _, _ = procMonitorFromWindow.Call(0, uintptr(MonitorDefaultToNearest))
	}
	if hMon == 0 {
		return 96, 96
	}
	var x, y uint32
	if procGetDpiForMonitor.Find() != nil {
		return 96, 96
	}
	ret, _, _ := procGetDpiForMonitor.Call(hMon, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)))
	if ret != 0 || x == 0 || y == 0 {
		return 96, 96
	}
	return uint(x), uint(y)
}

// ScaleFactor returns the DPI scale factor for the monitor containing r.
func ScaleFactor(r RECT) float64 {
	x, _ := MonitorDpi(r)
	if x == 0 {
		x = 96
	}
	return float64(x) / 96.0
}

// ScaleInt scales an integer value by the monitor's DPI factor, rounding.
func ScaleInt(value int32, r RECT) int32 {
	return int32(math.Round(float64(value) * ScaleFactor(r)))
}

// EnablePerMonitorDpiAwareness enables per-monitor DPI awareness V2 for the process
// so windows, appbars, and WebView2 surfaces render crisply on mixed-DPI displays.
func EnablePerMonitorDpiAwareness() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
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
