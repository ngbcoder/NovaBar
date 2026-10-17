# M5 findings: the GSMTC media bridge

Notes from actually trying to reach `GlobalSystemMediaTransportControlsSessionManager`
from Go. Most of this is about failures, because the failures were the expensive part.

## Short version

- **`go-ole` v1.3.0 is pure Go.** The plan to relax cgo for `internal/media` was based on
  a false premise, so the no-cgo constraint still holds tree-wide.
- Activation is implemented and tested. **Session property reads are not**, because they
  cannot be exercised on this machine and untested vtable marshalling crashes rather than
  returning an error.
- On this machine the OS refuses activation entirely with `REGDB_E_CLASSNOTREG`, via all
  three documented routes.

## 1. go-ole does not need cgo

The stated belief, in the README and in `bridge_windows.go`, was that reaching a WinRT
class needs go-ole and go-ole needs cgo, so the bridge was impossible without breaking
`CGO_ENABLED=0`. Relaxing the constraint was the proposed trade-off.

`go-ole@v1.3.0` has:

- no `import "C"`
- no `.c`, `.h` or `.syso` files
- `modcombase = windows.NewLazySystemDLL("combase.dll")`

It loads the DLL through `x/sys/windows`, exactly like the rest of this tree. `go.mod`
now lists it as a direct dependency and `build.ps1 verify` builds the whole tree with
`CGO_ENABLED=0` including it.

Older go-ole versions did require cgo, which is the likely origin of the belief. Worth
remembering as a general lesson: the tempting move when a COM dependency appears to need
cgo is to relax the constraint, and relaxing a safety property is far harder to undo than
adding a dependency. Checking was cheaper than the trade-off.

## 2. The hand-rolled attempt failed for a mundane reason

The first attempt called `combase.dll` directly. It returned `ERROR_INVALID_PARAMETER`
(87) for *every* class tried, including `Windows.Foundation.Uri`. That is the signature
of a marshalling bug, so the conclusion was that WinRT needed hand-written ABI work.

The actual bug: it called `CoInitializeEx`. That sets up a **COM apartment**. It does not
initialize **WinRT**. The call that is needed is `RoInitialize`, and
`RoGetActivationFactory` returns `ERROR_INVALID_PARAMETER` when the thread was never
brought up in the WinRT runtime.

`HSTRING` marshalling was fine throughout; that was not the problem either. `RoActivateInstance`
with a hand-rolled `ROACTIVATIONRO` struct segfaulted, but only because the struct layout
was wrong and the correct signature takes a class *name*, not a GUID.

go-ole exposes `RoInitialize`, `RoActivateInstance`, `RoGetActivationFactory`,
`NewHString` and `DeleteHString`, which is the entire surface needed here.

## 3. S_FALSE is reported as a spurious error

`RoInitialize` returns `S_FALSE` (1) when the thread is already initialized. go-ole wraps
any non-zero HRESULT via `NewError`, and the error formatter treats the value as a Win32
errno — so `S_FALSE` becomes `Errno(1)`, reported as **"Incorrect function."**

This is the same class of bug as the known-broken `x/sys/windows.CoInitializeEx`, which
must not be used.

The consequence is that an unfiltered `if err != nil` makes the bridge claim WinRT failed
on a perfectly healthy thread. `isHarmlessROError` filters it on the HRESULT rather than
the message, and there is a test pinning that.

## 4. This machine has no usable GSMTC stack

All three documented activation routes fail identically:

| Route | Result |
| --- | --- |
| `RoActivateInstance(className)` | `REGDB_E_CLASSNOTREG` |
| `RoGetActivationFactory(className, IID_IGlobalSystemMediaTransportControlsSessionManager)` | `REGDB_E_CLASSNOTREG` |
| `CoCreateInstance(CLSID_GlobalSystemMediaTransportControlsSessionManager, IID_...)` | `REGDB_E_CLASSNOTREG` |

The OS is Windows 11 25H2 (build 26200.9711), Professional, where GSMTC is expected to
exist. So this is not obviously a stripped image, and it is not a marshalling bug — three
independent APIs agree.

Note on registry archaeology: searching
`HKLM:\SOFTWARE\Classes\ActivatableClasses` for the class returns nothing, but so does a
control search for `Windows.Foundation.Uri`, which means that location is simply the
wrong place to look. The registry tells you nothing here; the runtime's own HRESULT does.

`classifyActivationError` names `REGDB_E_CLASSNOTREG`, `E_NOINTERFACE` and
`E_ACCESSDENIED` separately, because whether this is fixable is the only thing the user
cares about and a single opaque "activation failed" hides it.

## 5. What is deliberately not implemented

Reading a session's title, artist, album, playback status and timeline needs
`IPropertySet`/`IMapView` property access, plus `IVectorView` to enumerate sessions and
`IMediaPlaybackControl` to drive transport — several more hand-written vtables on top of
the activation that now works.

That code is **not written**, deliberately. It cannot be exercised on a machine where
activation is refused, and a mistake in hand-rolled vtable marshalling terminates the
process rather than returning an error — demonstrated, when `RoActivateInstance` with a
bad struct produced a `0xc0000005` access violation inside the Go runtime.

A crash in `NewBridge` would hit the daemon's pinned message-pump thread, which is
exactly what the watchdog exists to prevent. Untestable code that can crash the shell is
worse than an honest "not yet".

So `Current` returns `errNoSession` and `Command` refuses, and `Available()` is `false`
even when the manager is attached — because a bridge that cannot read a session is not
available, and reporting `true` would render an empty media area that looks like a bug
rather than unfinished work.

### To finish it

1. Confirm activation succeeds somewhere GSMTC is present. Until that happens the read
   path cannot be tested, and this is the blocking unknown.
2. Prefer `IPropertySet` over `IMapView`; the former is the documented way to read GSMTC
   state and has one fewer interface to marshal.
3. Use `ole.NewCallback` for the `CurrentSessionChanged` event, as
   `wailsapp/go-webview2` does for its own COM callbacks. It is the only proven pattern in
   this tree.
4. Poll rather than subscribe at first. Polling is testable against a known session and
   cannot leak a callback across a thread boundary; event handling can.
5. Register a COM callback object on a thread that stays pinned. The COM apartment is
   per-thread, so a callback delivered to a GC'd thread is a crash.