package win

import (
	"fmt"

	"golang.org/x/sys/windows"
)

var (
	windowsLazyOle32      = windows.NewLazySystemDLL("ole32.dll")
	procOleCoInitializeEx = windowsLazyOle32.NewProc("CoInitializeEx")
	procOleCoUninitialize = windowsLazyOle32.NewProc("CoUninitialize")
)

// HRESULT values for apartment initialization.
const (
	// hrOK: the apartment was initialized.
	hrOK uint32 = 0x00000000
	// hrFalse: COM was already initialized on this thread in a compatible apartment.
	// Success, not failure.
	hrFalse uint32 = 0x00000001
	// hrChangedMode: the thread already used COM in an incompatible apartment.
	// Compared as uint32 because 0x80010106 does not fit a signed int32 constant.
	hrChangedMode uint32 = 0x80010106
)

// coInitApartmentThreaded is COINIT_APARTMENTTHREADED, declared here rather than taken
// from x/sys so this file does not depend on that package's COM helpers, whose
// CoInitializeEx is wrong (see CoInitializeApartment).
const coInitApartmentThreaded = 0x2

// CoInitializeApartment puts the calling thread into a single-threaded apartment for
// COM, which WebView2 requires.
//
// This exists rather than windows.CoInitializeEx because the x/sys wrapper is wrong for
// COM. It treats the return value as a Win32 error code:
//
//	func CoInitializeEx(reserved uintptr, coInit uint32) (ret error) {
//	    r0, _, _ := syscall.SyscallN(...)
//	    if r0 != 0 { ret = syscall.Errno(r0) }
//	    return
//	}
//
// But COM returns an HRESULT, and S_FALSE is 1. So when COM is already initialized in a
// compatible apartment, which is the normal case by the time a surface is attached, the
// wrapper returns Errno(1), which Windows spells "Incorrect function". It reports
// failure exactly when things are fine.
//
// Returns alreadyInitialized so the caller can tell first-time init from a repeat,
// and an error only when the thread genuinely cannot host COM.
func CoInitializeApartment() (alreadyInitialized bool, err error) {
	r1, _, _ := procOleCoInitializeEx.Call(0, uintptr(coInitApartmentThreaded))

	switch uint32(r1) {
	case uint32(hrOK):
		return false, nil
	case uint32(hrFalse):
		return true, nil
	case hrChangedMode:
		return false, fmt.Errorf(
			"this thread already initialized COM in an incompatible apartment (RPC_E_CHANGED_MODE); " +
				"WebView2 needs a single-threaded apartment")
	default:
		return false, fmt.Errorf("CoInitializeEx failed with HRESULT %#08x", uint32(r1))
	}
}

// CoUninitialize releases the apartment if this call was the one that created it.
//
// Only call it when CoInitializeApartment reported alreadyInitialized == false. COM
// reference counts per thread, so an extra CoUninitialize unbalances it and the next
// COM object creation on the thread fails in ways that are hard to attribute.
func CoUninitialize() {
	procOleCoUninitialize.Call()
}
