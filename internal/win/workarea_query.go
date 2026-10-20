package win

import "unsafe"

// SystemParametersInfo action and flags for reading the desktop work area.
const (
	spiGetWorkArea = 0x0030
)

// GetWorkArea returns the desktop work area: the screen minus any reserved AppBar strips
// and the taskbar.
//
// Read with SPI_GETWORKAREA rather than the monitor enumeration because it reflects what
// Windows actually believes the usable desktop is, which is what a floating window should
// sit above. Zero RECT on failure, so callers can fall back to the screen metrics.
func GetWorkArea() RECT {
	var r RECT
	ok, _, _ := procSystemParametersInfoW.Call(
		uintptr(spiGetWorkArea),
		0,
		uintptr(unsafe.Pointer(&r)),
		0)
	if ok == 0 {
		return RECT{}
	}
	return r
}

// WorkAreaBottom returns the bottom edge of the work area, or the screen height when the
// query fails.
func WorkAreaBottom() int32 {
	if r := GetWorkArea(); r.Bottom > 0 {
		return r.Bottom
	}
	return int32(GetSystemMetrics(1))
}

// WorkAreaLeft returns the left edge of the work area, or zero when the query fails.
func WorkAreaLeft() int32 {
	if r := GetWorkArea(); r.Right > r.Left {
		return r.Left
	}
	return 0
}
