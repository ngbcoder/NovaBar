# M5 findings: the GSMTC media bridge

Notes from actually trying to reach `GlobalSystemMediaTransportControlsSessionManager`
from Go. Most of this is about failures, because the failures were the expensive part.

## Short version

- **`go-ole` v1.3.0 is pure Go.** The plan to relax cgo for `internal/media` was based on
  a false premise, so the no-cgo constraint still holds tree-wide.
- **GSMTC was never unavailable.** An earlier version of this file blamed the machine; the
  real cause was a wrong class name. Section 4 is the post-mortem.
- Activation now works and the bridge attaches. **Session property reads are not written**,
  because they need five more hand-written vtables plus an async property round trip, and a
  wrong slot terminates the process rather than returning an error.
- `windows-rs` was evaluated as a way to avoid that marshalling and **is Rust-only**; there
  are no Go bindings.

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

## 4. GSMTC was never unavailable — the class name was wrong

**This section previously said the opposite. It was wrong.**

Every probe used:

```
Windows.GlobalSystemMediaTransportControls.GlobalSystemMediaTransportControlsSessionManager
```

That class does not exist. The namespace is **`Windows.Media.Control`**:

```
Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager
```

With the corrected name, `RoGetActivationFactory` returns a valid object immediately. The
bridge now attaches.

### Why the mistake was expensive

A wrong activatable class name and an absent one are **indistinguishable at the API
boundary**: both return `REGDB_E_CLASSNOTREG`. So `REGDB_E_CLASSNOTREG` was read as
evidence about the environment, and a chain of investigation followed that was all
reasonable and all chasing the wrong thing:

- concluded this Windows build has no media session stack
- checked OS edition and build and the audio services, which are all fine
- searched the registry for the activatable class, found nothing, and nearly believed it.
  The same scan also failed to find `Windows.Foundation.Uri`, which definitely activates,
  so the location was simply wrong
- formed an app-identity hypothesis because the failing classes looked capability-gated,
  then disproved it: `MediaPlayer` is not identity-gated yet is refused, while
  `MediaSource` works

Seven of twelve probed WinRT classes activated fine. That should have been the signal that
the environment was healthy and the failing names were the problem.

### The check that would have caught it

Before concluding anything about a platform from a single "not found" result, confirm the
identifier exists. `REGDB_E_CLASSNOTREG` answers "is this class name registered", not "is
this feature present". The cheap check is to ask for `IID_IUnknown` first:

```go
// If this fails, the class name is wrong.
ole.RoGetActivationFactory(className, iidUnknown)
```

`internal/media` does exactly this now, and requests `IID_IUnknown` rather than a typed
interface recalled from documentation — an unverified IID is the same mistake one level
down.

### Verified facts for whoever resumes this

| Fact | Value |
| --- | --- |
| Class name | `Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager` |
| `RoActivateInstance` | `E_NOTIMPL` — not directly activatable |
| `RoGetActivationFactory(…, IID_IUnknown)` | succeeds |
| Acquiring the manager | requires the static `RequestAsync()`, so an `IAsyncOperation` and its `GetResults` |
| Reading a session | `GetCurrentSession()`/`GetSessions()` on the manager; `GetPlaybackInfo()`, `GetTimelineProperties()`, `TryPlayAsync()`, `TryPauseAsync()`, `TryGetMediaPropertiesAsync()` on the session |

The public runtime class exposes only `GetCurrentSession`, `GetSessions` and `RequestAsync`.
`GetPlaybackInfo`/`GetProperties` live on the `IGlobalSystemMediaTransportControlsSessionManager`
interface. Media properties (title, artist, album) need an
`IAsyncOperation<IPropertySet>` — an async round trip, not a synchronous getter.

One trap already hit: `RoGetActivationFactory(…, IID_IUnknown)` does **not** hand back the
`IActivationFactory` vtable. It returns the class's *default interface*, so calling slot 6
expecting `ActivateInstance` faults at `0xffffffffffffffff` rather than returning
`E_NOTIMPL`. Reach the manager through `RequestAsync`, not by poking the factory vtable.

`classifyActivationError` still names `REGDB_E_CLASSNOTREG`, `E_NOINTERFACE` and
`E_ACCESSDENIED` separately, because whether a failure is fixable is the only thing a user
can act on.

## 5. What is deliberately not implemented

Reading a session's title, artist, album, playback status and timeline needs
`IPropertySet`/`IMapView` property access, plus `IVectorView` to enumerate sessions and
`IMediaPlaybackControl` to drive transport — several more hand-written vtables on top of
the activation that now works.

That code is **not written**, deliberately. It needs at least five more vtables —
`IActivationFactory`, `IAsyncOperation`, the manager interface, the session interface and
`IPropertySet`/`IMap` — and a mistake in hand-rolled marshalling terminates the process
rather than returning an error, demonstrated twice: a bad `ROACTIVATIONRO` struct produced
a `0xc0000005` access violation inside the Go runtime, and assuming the factory exposed
`IActivationFactory` faulted at `0xffffffffffffffff`.

`windows-rs` would have supplied metadata-generated bindings and removed that risk
entirely. It is Rust-only; there is no Go equivalent, so the ABI has to be written by hand.

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