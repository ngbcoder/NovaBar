//go:build windows

// Package winrt provides the small slice of the COM and WinRT ABI that the media
// bridge needs.
//
// It exists because there is no Go projection of the Windows Runtime, and the parts
// that are needed are small enough to write directly: a handful of interface
// pointers, a dozen vtable slots, and some strings.
//
// Two rules from the ABI are load bearing and easy to get wrong, so they are spelled
// out here rather than left to each call site:
//
//   - A COM object pointer points at a block whose first word is the vtable pointer.
//     The vtable is read through that pointer, never by indexing the object itself.
//     Indexing the object reads the vtable pointer and the reference count as if they
//     were methods, which faults.
//
//   - Every WinRT method returns an HRESULT and passes its result through an out
//     parameter. Nothing is returned in a register, so a call that forgets the out
//     parameter reads whatever was on the stack.
//
// Calls go through the platform's own call mechanism rather than a Go function
// value, because a Go function value uses the Go calling convention, which is not the
// one the system libraries were compiled with.
package winrt

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

// ptrSize is the size of a pointer on this platform.
const ptrSize = unsafe.Sizeof(uintptr(0))

// HRESULT is a Windows result code.
type HRESULT uint32

// Failed reports whether the code indicates failure.
func (h HRESULT) Failed() bool { return uint32(h)&0x80000000 != 0 }

// Succeeded reports whether the code indicates success.
func (h HRESULT) Succeeded() bool { return !h.Failed() }

// Error renders the code.
func (h HRESULT) Error() string {
	return fmt.Sprintf("HRESULT 0x%08x", uint32(h))
}

// Common result codes worth naming.
const (
	// ENoInterface means the object does not implement the requested interface.
	ENoInterface HRESULT = 0x800401F0
	// EPointerNull means a method was called on an empty object.
	EPointerNull HRESULT = 0x80004003
	// EUnexpected means the object did not behave as the ABI requires.
	EUnexpected HRESULT = 0x8000FFFF
	// EChangedState means the object can no longer answer the question.
	EChangedState HRESULT = 0x8000000C
)

// Object is a COM or WinRT interface pointer.
//
// It is a plain pointer holder rather than a finalizer-backed wrapper on purpose:
// these objects are released explicitly, on the thread that owns the apartment they
// were created in.
type Object struct {
	ptr unsafe.Pointer
}

// New wraps a raw interface pointer.
func New(ptr unsafe.Pointer) Object { return Object{ptr: ptr} }

// IsNil reports whether the object is empty.
func (o Object) IsNil() bool { return o.ptr == nil }

// Pointer returns the raw interface pointer.
func (o Object) Pointer() unsafe.Pointer { return o.ptr }

// Call invokes a method by vtable slot and returns the result code.
//
// Slot numbers are the ABI's own: IUnknown occupies slots 0 to 2, IInspectable 3 to
// 5, and an interface's own methods start at slot 6. Results are passed through out
// parameters, so the arguments are the addresses of the caller's variables.
func (o Object) Call(slot int, args ...uintptr) HRESULT {
	if o.ptr == nil {
		return EPointerNull
	}

	// The object pointer points at a block whose first word is the vtable pointer.
	// Reading the vtable through that pointer is the only correct way to reach it.
	vtbl := *(*unsafe.Pointer)(o.ptr)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*ptrSize))

	callArgs := make([]uintptr, 0, len(args)+1)
	callArgs = append(callArgs, uintptr(o.ptr))
	callArgs = append(callArgs, args...)

	r1, _, _ := syscall.SyscallN(fn, callArgs...)
	return HRESULT(uint32(r1))
}

// QueryInterface asks the object for another interface.
//
// The result is a new reference, so it has to be released like any other.
func (o *Object) QueryInterface(iid *ole.GUID) (Object, error) {
	var out unsafe.Pointer
	hr := o.Call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if hr.Failed() {
		return Object{}, hr
	}
	if out == nil {
		return Object{}, EUnexpected
	}
	return Object{ptr: out}, nil
}

// Release drops the object's reference.
//
// It is safe to call on an empty object.
func (o *Object) Release() {
	if o.ptr == nil {
		return
	}
	o.Call(2)
	o.ptr = nil
}

// HString is a WinRT string handle.
type HString = ole.HString

// ReadHString converts a string handle to a Go string and releases the handle.
//
// An empty handle is an empty string, not an error: WinRT uses it to mean "no
// value".
func ReadHString(h HString) string {
	if h == 0 {
		return ""
	}
	s := h.String()
	ole.DeleteHString(h)
	return s
}

var procRoUninitialize = windows.NewLazySystemDLL("combase.dll").NewProc("RoUninitialize")

// RoUninitialize tears down the WinRT apartment on the calling thread.
//
// It pairs with ole.RoInitialize. Failing to call it leaks the apartment for the
// life of the thread, which is survivable but untidy.
func RoUninitialize() {
	procRoUninitialize.Call()
}
