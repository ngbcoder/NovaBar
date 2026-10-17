package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetProcessDescription = modKernel32.NewProc("SetProcessDescription")

// SetProcessDescription labels the current process as it appears in Task Manager and
// other process tools.
//
// The watchdog shares an image name with the daemon, so "end task" on every NovaBar
// process takes the safety net down along with the thing it protects. Labelling it
// gives the user something to leave alone.
//
// Purely cosmetic, so it must never be able to hurt the caller. Two guards, both of
// which this function needs after the alternative was tried and found:
//
//  1. The proc is resolved before calling. syscall.LazyProc.Call *panics* when the
//     symbol is absent, and this one lives in kernelbase.dll rather than kernel32.dll,
//     relying on export forwarding. A panic here killed the watchdog outright, which
//     removed the recovery path entirely, so a missing export now means "skip".
//  2. The call is wrapped in a recover, because the alternative to a cosmetic label is
//     never worth an unwinding panic.
func SetProcessDescription(description string) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()

	if procSetProcessDescription.Find() != nil {
		return false
	}

	desc, err := windows.UTF16PtrFromString(description)
	if err != nil {
		return false
	}

	// The first argument is ignored; the current process is always described.
	r1, _, _ := procSetProcessDescription.Call(
		uintptr(unsafe.Pointer(nil)),
		uintptr(unsafe.Pointer(desc)),
	)
	return r1 != 0
}
