package win

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procShellExecuteW opens a document or protocol URI with the shell.
var procShellExecuteW = modShell32.NewProc("ShellExecuteW")

// ShellExecute opens target with the shell's default handler.
//
// This is the right entry point for things that are not executables: protocol URIs
// such as ms-settings:network-wifi and documents Windows has an association for. A
// process cannot be created from those directly, so CreateProcess rejects them.
func ShellExecute(target string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}

	// ShellExecuteW returns a value greater than 32 on success; anything at or below
	// it is an error code rather than an HINSTANCE.
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		uintptr(SW_SHOWNORMAL),
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecute(%q) failed with code %d", target, ret)
	}
	return nil
}
