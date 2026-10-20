# M5 findings: the GSMTC media bridge

Notes from reaching `GlobalSystemMediaTransportControlsSessionManager` from Go. Most
of this is about failures, because the failures were the expensive part.

**This is finished.** The bridge reads sessions, playback state and transport
capabilities, and drives the transport. What follows is how it got there, and what was
wrong along the way, because the wrong parts are the ones worth keeping.

## Short version

- **`go-ole` v1.3.0 is pure Go.** The plan to relax cgo for `internal/media` was based on
  a false premise, so the no-cgo constraint still holds tree-wide.
- **GSMTC was never unavailable.** An earlier version of this file blamed the machine; the
  real cause was a wrong class name. Section 4 is the post-mortem.
- **The missing IID was in the metadata the operating system ships with.** Not in the
  GUID heap, which holds one entry, but in a `GuidAttribute` on each interface. Section 5.
- **Nothing is written from documentation any more.** `internal/winmd` reads the metadata,
  and a test fails if a constant and the metadata disagree.
- **The media properties are not a property bag.** The session hands back an object with
  the title, artist and album on it. Section 7.
- **`windows-rs` was evaluated and is Rust-only**; there are no Go bindings.

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

## 5. The missing IID was in the metadata all along

This is the part that had everyone looking in the wrong place. The blocker was the IID of
`IGlobalSystemMediaTransportControlsSessionManagerStatics`, and the previous version of
this file said:

> The SDK header declares `IID_..._Statics = __uuidof(...)` but `__uuidof` resolves from
> `Windows.Media.winmd`, so the literal GUID is not in the header, and it did not surface
> in documentation searches.

And then:

> Where to get the IID cheaply: parse the `InterfaceDef` table's GUID heap from
> `Windows.Media.winmd`.

**There is no `InterfaceDef` table, and the GUID heap holds one entry.** WinRT metadata
is ECMA-335 metadata, and in ECMA-335 the GUID heap is for module and assembly
identifiers. An interface's identifier is not in it.

Where it is: **a `GuidAttribute` on the interface's type definition**, whose value blob
holds the identifier. The GUID heap's single entry is the `GuidAttribute` *type's* own
identifier.

The metadata the operating system ships with is at:

```
C:\Windows\System32\WinMetadata\Windows.Media.winmd
C:\Windows\System32\WinMetadata\Windows.Foundation.winmd
```

Reading it is a normal PE plus ECMA-335 metadata parse, which `internal/winmd` now does.
There is no need for `mdapi`, `DIA` or the SDK.

The identifier, for the record:

```
IGlobalSystemMediaTransportControlsSessionManagerStatics = 2050c4ee-11a0-57de-aed7-c97c70338245
```

## 6. The metadata also settles the slot order

The previous version of this file listed the vtable slots by hand, read out of an SDK
header that is not installed here. The metadata says the same thing, and says it in a form
that cannot be misread: an interface's methods appear in the order the vtable has them,
and the ABI puts the first one at slot 6.

So the slots are not written down either. `internal/media` names a method, the test looks
the method up in the metadata, and the slot constant is checked against it. The
declaration order is also how the interface's own method list is read, so the two cannot
disagree.

Two things the metadata corrected, both of which would have been wrong from memory:

- **`AsyncStatus` is not in value order.** The members are declared `Canceled, Completed,
  Error, Started` and their values are `2, 1, 3, 0`. Reading the values from the
  constants table is the only way to get them right.
- **`IAsyncOperation` does not derive from `IAsyncInfo`** in the metadata, so `GetResults`
  is at slot 8 and not at 13.

### Two bugs that cost most of the effort

**1. Reading the wrong array for the vtable.** go-ole's `IUnknown` is
`{ RawVTable *interface{} }`, and go-ole's own accessors use
`(*Vtbl)(unsafe.Pointer(v.RawVTable))`. An early probe read a `uintptr` array *at the object
address* instead, whose first word is `RawVTable` itself, so index 3 was not `GetIids` and
index 6 was not `ActivateInstance`. Both were garbage and both faulted at
`0xffffffffffffffff`. This was almost certainly the single biggest reason the earlier
attempts looked like deep ABI problems rather than an indexing mistake.

`internal/winrt` does it once, in one place, and says so in a comment.

**2. `GetIids` stride.** `IID** iids` in the ABI is a pointer to a *contiguous array of
16-byte IID structs*, not an array of pointers. Walking it with an 8-byte stride and
dereferencing each element reads garbage.

## 7. The media properties are not a property bag

The previous version of this file was sure that reading a session's title needed
`IPropertySet` and `IMapView`, "several more hand-written vtables on top of the
activation that now works", and that this was the largest remaining piece.

It is not. The metadata says:

```
IGlobalSystemMediaTransportControlsSession.TryGetMediaPropertiesAsync
    -> IAsyncOperation<GlobalSystemMediaTransportControlsSessionMediaProperties>
```

The session hands back a media properties object with `get_Title`, `get_Artist` and
`get_AlbumTitle` on it. One more interface, no property bag, no `IMapView`, and no
`IVectorView` unless the session list is wanted as well.

## 8. How it was finished

The chain, all of it read from the metadata:

1. `RoGetActivationFactory(className, staticsIID)` → the statics object
2. statics slot 6 `RequestAsync` → `IAsyncOperation<SessionManager>`
3. `IAsyncOperation` slot 8 `GetResults` → the manager
4. manager slot 6 `GetCurrentSession` → the session, or nil when nothing is playing
5. session slot 6 `get_SourceAppUserModelId` → the owning application
6. session slot 7 `TryGetMediaPropertiesAsync` → `GetResults` → title, artist, album
7. session slot 8 `GetTimelineProperties` → position and duration
8. session slot 9 `GetPlaybackInfo` → status and the transport capabilities
9. session slots 10, 11, 12, 16, 17, 20 → play, pause, stop, next, previous, toggle

Two decisions are worth writing down.

**One thread.** WinRT initializes per thread, and the daemon answers media over RPC on
whichever goroutine the request arrived on. The bridge owns a thread, initializes the
runtime on it, and runs every call there. A COM apartment is per-thread, and a callback
delivered to a thread without one is a crash.

**Polling, not events.** The stack publishes `CurrentSessionChanged`, `PlaybackInfoChanged`
and `MediaPropertiesChanged`, and the previous version of this file recommended using
`ole.NewCallback` for them. It does not: the daemon re-reads the session every couple of
seconds and broadcasts the result. Polling cannot fault, cannot deliver a callback to a
thread whose apartment has gone away, and at this interval nobody can tell the difference.

## 9. What is deliberately left out

- **Subscribing to the change events.** Polling covers it, and a callback is the part
  that can cross a thread boundary and fault. Revisit if the polling interval ever has to
  get much shorter.
- **The session list.** `GetSessions` is one slot away and is not used: the bar shows the
  current session, which is what the media area was for.
- **Thumbnails.** `get_Thumbnail` is an `IRandomAccessStreamReference`, which means
  another interface and a stream. The pill shows text.

## 10. How to verify it

`tools/mediaprobe` prints what the bridge can see, and takes a command to send:

```
go run ./tools/mediaprobe
go run ./tools/mediaprobe play
```

`tools/windump` prints the metadata itself, which is where every identifier and slot
came from:

```
go run ./tools/windump C:\Windows\System32\WinMetadata\Windows.Media.winmd Windows.Media.Control
```

And the tests in `internal/media` re-read the metadata and fail if a constant and the
metadata ever disagree. That is the whole defence: the wrong class name in this area
cost a day, and an identifier read from memory would have cost the same.
