# NovaBar — status and todo

A Windows 11 desktop shell and tiling window manager in pure Go. No cgo, no UAC, one
binary plus a control CLI.

This file is the handover: what works, what is half-built, what is next, and the traps
that cost real time so they are not rediscovered.

---

## Build and run

```powershell
.\build.ps1 verify    # build with CGO_ENABLED=0, vet, run the tests. This is the gate.
.\build.ps1 build     # bin/novabar.exe and bin/shellctl.exe
.\build.ps1 test      # build, then test
```

Running from source rather than from `bin/`:

```powershell
$env:NOVABAR_WIDGETS = '<repo>\widgets'
```

Config lives at `%APPDATA%\NovaBar\config.toml`. A copy of the current one is at
`config.toml.bak` next to it.

**When testing, kill only the daemon and let the watchdog run.** Killing both at once
removes the safety net and leaves a stale work area behind.

---

## Milestones

| Milestone | State |
| --- | --- |
| M1 Win32 core, safety foundations | Done |
| M2 Pure-function tiling engine | Done |
| M3 AppBar, workspaces, taskbar management | Done (multi-bar, multi-edge) |
| M4 WebView2 UI, widget platform | Done (bar, dock, launcher, settings widgets, manifest permissions) |
| M5 App launcher, GSMTC media | **Partial.** Launcher done. GSMTC activation works; session reads moved to M10. |
| M6 Settings menu | **Done.** Opens with `Ctrl+Alt+S`. Controls are generated from the config schema. |
| M7 Per-app volume | **Done.** Wheel over a dock tile adjusts that application's volume. |
| M8 Keyboard window management | **Next.** The biggest product gap: the tiling engine works and there is no way to drive it. |
| M9 Multi-monitor correctness | Planned. A bug, not a feature, and it blocks real multi-monitor use. |
| M10 GSMTC media reads | Planned. Finishes M5. |
| M11 Notifications | Planned. |
| M12 Clipboard manager | Planned. |
| M13 Persistence and theming | Planned. |
| M14 Full shell replacement | An **option, not a requirement**. |

The README carries the same table with a section per milestone describing what each one
means, and the table itself is **generated** from the Notion board by `tools/roadgen` and
kept in step by `.github/workflows/roadmap.yml`, which runs on a schedule and whenever
the generator changes.

**Labels** on each milestone are the cross-cutting themes, the ones that appear on more
than one milestone, so a question like "which milestones touch the widget platform?" is
one filter away.

The generator uses a Notion **internal integration token** (`secret_…`), not a personal
access token. An internal integration sees only the pages shared with it; a PAT would
grant the whole workspace, which is too much to put in a repository secret for a public
repo.

To set it up: create an internal integration with the *Read content* capability at
`notion.so/my-integrations`, share the "NovaBar Roadmap" database with it through the
database's `…` menu → Connections, then add the token as the repository secret
`NOTION_TOKEN`.

The sharing step is the one that gets missed, and its symptom is an empty result rather
than an error. The tool refuses to blank the README in that case rather than quietly
wiping the table.

**A Notion page cannot be embedded in a GitHub README.** Notion serves
`X-Frame-Options: SAMEORIGIN` and a `frame-ancestors` list containing only Notion's own
domains, so the browser refuses the frame even where the tag renders. An iframe was
tried and cannot work; the generated table is the working equivalent.

The board: <https://basalt-lancer-b02.notion.site/0a5506f72fd540acaa0a0123c9c29d80?v=3ffd677f1c2f817bb34f000ced4524cd>

### Shipped features

- Dock pinning, persisted to `%APPDATA%\NovaBar\pins.json`, atomic writes, one entry per
  application.
- Settings overlay whose controls are generated from `internal/config.Schema`, so a
  setting cannot be rendered that the daemon would refuse.
- Per-application volume through Core Audio, with the write path genuinely tested.
- macOS-style floating dock: translucent pill, magnification, running dots, separator
  between pinned apps and running windows.
- The native Windows taskbar is hidden and the floating dock replaces it.
- NovaBar icon on both binaries, and a wordmark banner in the README.

---

## In flight: the Windows key opens our own app menu

**Goal.** Pressing the Windows key opens NovaBar's own application menu instead of the
Windows Start menu.

**Measured, which is why this is worth doing.** A probe of `RegisterHotKey` on Windows 11
build 26200:

| Combination | Result |
| --- | --- |
| **Win alone** | **ACQUIRED** — Windows does not claim it |
| Ctrl+Esc | Refused, already registered |
| Win+Space | Refused, already registered |
| Ctrl+Alt+S | Acquired |

So the bare Windows key is available to an application. `Ctrl+Esc` is not.

**Done so far**

- `internal/win/win32.go` — `VK_LWIN = 0x5B` added.
- `internal/win/hotkey.go` — `ParseHotkeySpec` accepts a modifier-only spec, but only for
  the Windows key. The other bare modifiers stay refused on purpose: a bare Ctrl or
  Shift hotkey would swallow `Ctrl+C` and `Ctrl+Shift+I`, which is much worse than not
  having the binding.

**Not done — this is the remaining work**

1. `internal/config/config.go` — add `Hotkeys.AppMenu string \`toml:"app_menu"\`` to
   `HotkeyConfig`, default `"Win"`, and to `DefaultConfig`.
2. `internal/win/win32.go` — add a `HotkeyAppMenu` registration id.
3. `internal/hotkey/manager.go` — add `ActionToggleAppMenu` and register the binding.
4. `cmd/daemon/main.go` — dispatch it to open the launcher, which is already the
   application menu.
5. `internal/config/schema.go` — expose `hotkeys.app_menu` so it appears in settings.
6. **Make the launcher look like a Start menu.** It already searches applications and
   windows, but it presents as a search box. It wants a pinned row, an "All apps" list and
   a power/actions row.
7. **Verify the Start menu is actually suppressed.** Binding the Win key should stop the
   shell opening its own Start menu, but that is not guaranteed and has to be checked on a
   real desktop.

**Open risk.** Registering the bare Windows key may or may not stop the shell's Start menu
opening, because the shell handles a bare Win keypress through a path that is not the
same as a registered hotkey. If it does not, the fallback is a low-level keyboard hook
(`WH_KEY_LDBLL_HOOK`) that swallows a bare Win tap and opens the menu while passing
`Win+<key>` combinations through untouched. That is more invasive and has to be written
carefully, or `Win+E`, `Win+Tab` and `Win+R` all stop working.

---

## M8: keyboard window management (the next milestone)

**The tiling engine works and there is no way to drive it.** The only hotkeys are
rescue, launcher, settings and the two workspace steps. You cannot focus a window in a
direction, move one, resize one, or move one to another workspace.

Planned, all from a configurable keymap:

| Keys | Action |
| --- | --- |
| `Win+H` `J` `K` `L` | Focus the window to the left, down, up, right |
| `Win+Shift+H` `J` `K` `L` | Move the focused window |
| `Win+Ctrl+H` `J` `K` `L` | Resize the focused window |
| `Win+1`…`Win+9` | Jump to a workspace |
| `Win+Shift+1`…`9` | Move the focused window to a workspace |

Notes for whoever picks this up:

- `internal/workspace` already has `Move(hwnd, targetID)` and `Assign`, so moving a
  window between workspaces does not need new plumbing in the workspace manager.
- `internal/tiling` is pure functions over `WindowSubject` and `Placement`, so focus and
  move are most naturally expressed there and applied by the daemon.
- The keymap belongs in `config.HotkeyConfig` and therefore in the settings schema, so it
  is editable without touching code.
- `Win+Shift+H` and friends must not be swallowed by the application-menu hotkey work
  below. The bare Windows key is one binding; `Win` plus a key is a different one, and
  the bare-key registration must not take those with it.
- `windows.focus` already exists as a permission, and the `windows.control` split means
  a widget can focus without being able to move or close.

---

## M9: multi-monitor correctness

Bar geometry uses `GetSystemMetrics`, which reports the **primary display only**, rather
than `MONITORINFO` per monitor. A window dragged to a second screen lands in the wrong
place, and per-monitor work areas are not honoured.

`internal/tiling` already has `MonitorInfo` and `monitorIndexFor`, and `detectMonitorsLocked`
exists, so the engine has the shape of this. The bar manager is where the fix belongs.

Recorded as open in `docs/webview2-m4-findings.md` §5 before it was promoted to a
milestone.

---

---

## M10: GSMTC session reads (finishes M5)

The bar's transport controls are wired but the media area stays hidden, because nothing
reads a session yet. This milestone makes the media area real: title, artist, playback
state, position, and working play/pause/next/previous.

**Activation works.** `Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager`
activates. The bridge attaches and reports that reads are unimplemented rather than
pretending otherwise.

**What is still needed** is one identifier and then a chain of hand-written vtable calls:

| Interface | Slot | Member |
| --- | --- | --- |
| `IGlobalSystemMediaTransportControlsSessionManagerStatics` | 6 | `RequestAsync` → `IAsyncOperation<SessionManager>` |
| `IAsyncOperation<T>` | 8 | `GetResults` |
| `IGlobalSystemMediaTransportControlsSessionManager` | 6 | `GetCurrentSession()` |
| `IGlobalSystemMediaTransportControlsSession` | 6 | `get_SourceAppUserModelId` |
| | 7 | `TryGetMediaPropertiesAsync` (async) |
| | 8 | `GetTimelineProperties` |
| | 9 | `GetPlaybackInfo` |
| | 10 | `TryPlayAsync` |
| | 11 | `TryPauseAsync` |
| | 12 | `TryStopAsync` |

Slots are the declaration order inside each `...Vtbl` struct in the installed SDK header:
`C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\winrt\Windows.Media.Control.h`.

**The one missing piece is the IID of the statics interface.** The header declares it as
`__uuidof(...)`, which resolves from `Windows.Media.winmd`, so the literal GUID is not in
any header and did not surface in documentation searches. Parse the `InterfaceDef` table's
GUID heap from the winmd, or use metadata tooling. **Do not guess it** — an unverified IID
fails as `E_NOINTERFACE`, which reads plausibly enough to mislead, and that is exactly
how this task already went wrong once (see traps).

Three further vtables are needed for `GetPlaybackInfo` and `GetTimelineProperties`:
`GlobalSystemMediaTransportControlsSessionPlaybackInfo`,
`GlobalSystemMediaTransportControlsSessionPlaybackControls` and
`GlobalSystemMediaTransportControlsSessionTimelineProperties`. Their slots are in the same
header. Media properties — title, artist, album — need `IPropertySet` and an async
property read, which is the largest remaining piece.

**Why it is not written.** Every step is a raw vtable call, and a wrong signature
terminates the process rather than returning an error. That happened three times. A crash
in `NewBridge` lands on the daemon's pinned message-pump thread, which is what the watchdog
exists to prevent. Untestable code that can kill the shell is worse than an honest "not
yet".

`windows-rs` would have supplied metadata-generated bindings and removed the risk
entirely. It is Rust-only; there are no Go bindings. `go-ole` does the activation, which
is why it is a dependency, but it does not generate vtables.

---

## Smaller things still open

- **M6: gap and layout changes do not re-tile windows that are already open.** Live
  reload covers bar geometry and taskbar visibility, and `config.set` calls `retile`, but a
  layout change does not move existing windows until something else triggers a retile.
- **M6: rules are not editable in the settings UI.** Deliberate. They are a list of
  records with compound matching and do not fit a generated form; editing them stays a
  `config.toml` job.
- **M6: there is no `windows.focus` equivalent for the launcher** — actually there is.
  `windows.focus` exists and both widgets now declare it. No action.
- **M5: the media area stays hidden** until the GSMTC reads above are done.
- **Multi-monitor AppBars.** `updatePositionLocked` uses `GetSystemMetrics`, which is the
  primary display only, not `MONITORINFO` per monitor. Recorded as open in
  `docs/webview2-m4-findings.md` §5.

---

## Needs a decision: where the config file lives

**This is currently in an inconsistent state and `build.ps1 verify` is red because of it.**

Commit `1be38d6` ("Added MacOS style dock") changed `config.GetConfigPath()` from
`%APPDATA%\NovaBar\config.toml` to a relative `"config.toml"` — "returns Project Root".

That was a deliberate change, and a reasonable one for a portable or development setup.
But it was made in one place only, and the project now has three subsystems that disagree
about where state lives:

| What | Where it is now |
| --- | --- |
| `config.toml` | relative to the process's working directory |
| `pins.json` | `%APPDATA%\NovaBar\pins.json` |
| `ipc.json` (the discovery file `shellctl` and widgets read) | `%APPDATA%\NovaBar\ipc.json` |

**Consequences, all of which are observable:**

- The `%APPDATA%\NovaBar\config.toml` that the README documents, and that the settings
  surface and the pins are next to, is **ignored**. Editing it has no effect.
- The config's location depends on **where the process was started from**. A shell
  launched from Explorer, from a terminal, and from a shortcut can read three different
  files.
- `internal/config` has **two failing tests** — `TestLoadCreatesDefaultOnFirstRun` and
  `TestLoadFallsBackToSafeConfigOnBrokenFile` — because they still expect the `%APPDATA%`
  path. `build.ps1 verify` fails because of this.
- `hide_taskbar` in the `%APPDATA%` config had no effect. The native taskbar was hidden
  only because the **default** changed to `true`. A user who sets it back to `false` in
  `%APPDATA%` will not see that honoured.

**Two coherent ways to finish this. Pick one.**

1. **Put it back in `%APPDATA%`.** `GetConfigPath` returns
   `%APPDATA%\NovaBar\config.toml` again, and the two tests pass unchanged. All three
   subsystems then agree. This is what the README documents.
2. **Make "project root" a real, single mode.** One function resolves a root directory —
   from an environment variable, or the directory the executable sits in — and the config,
   the pin store and the IPC descriptor all use it. The tests then set that one root
   rather than `APPDATA`. This is the better design if a portable install is wanted, and
   it is the more work.

Option 2 is worth doing properly if a portable mode is the goal, because half-applying it
is what produced this state. Option 1 is a one-line revert and is correct if it was not.

**Until this is settled, editing the config in `%APPDATA%` does nothing**, which is worth
knowing before spending time on it.

---

## Traps

These cost real time. Each one is a bug that was live at some point, or a wrong turn that
took a long while to unwind.

**A wrong class name and an absent one are indistinguishable.** `REGDB_E_CLASSNOTREG`
answers "is this class name registered", not "is this feature present". GSMTC was
unavailable for a long stretch because the class name used
`Windows.GlobalSystemMediaTransportControls.…` when the namespace is `Windows.Media.Control`.
Every conclusion drawn from that HRESULT about the machine, the OS edition, the registry
and app identity was wrong. **Before concluding anything about the platform from a single
"not found", confirm the identifier exists** — ask for `IID_IUnknown` first.

**Read the vtable at the right place.** go-ole's `IUnknown` is `{ RawVTable *interface{} }`
and its accessors use `(*Vtbl)(unsafe.Pointer(v.RawVTable))`. Reading a `uintptr` array at
the *object* address makes index 0 `RawVTable` itself, so index 3 is not `GetIids` and
index 6 is not `ActivateInstance`. Both were garbage and both faulted at
`0xffffffffffffffff`. This was the single biggest reason the GSMTC work looked like deep
ABI problems rather than an indexing mistake.

**`GetIids` returns a contiguous array of 16-byte IID structs**, not an array of pointers.
Walking it with an 8-byte stride and dereferencing each element reads garbage.

**A hand-rolled vtable call kills the process.** It does not return an error. Assume any
new ABI code can crash the shell until proven otherwise, and test it in a sacrificial
process first.

**`go-ole` v1.3.0 is pure Go.** It has no `import "C"`, no `.c`, `.h` or `.syso` files, and
loads `combase.dll` through `x/sys/windows`. Older releases needed cgo, which is where the
belief came from. The no-cgo promise still holds tree-wide, including the media bridge.

**`RoInitialize` is not `CoInitializeEx`.** Setting up a COM apartment does not initialize
WinRT. Calling `RoGetActivationFactory` after only `CoInitializeEx` returns
`ERROR_INVALID_PARAMETER` for *every* class, including trivially registered ones, which
looks exactly like a marshalling bug.

**`S_FALSE` is reported as a spurious error.** `RoInitialize` returns `S_FALSE` (1) when
the thread is already initialized, and the go-ole error formatter reads the HRESULT as a
Win32 errno, so it surfaces as "Incorrect function". Filter it on the HRESULT, not the
message. The same class of bug affects `x/sys/windows.CoInitializeEx`, which must not be
used.

**`RegisterHotKey(hwnd, id, fsModifiers, vk)`.** The argument order matters and a wrong
one reports "Invalid flags" for every combination, including ones that work.

**`VirtualKeyName` must invert what the parser accepts.** Letters and digits are accepted
on the way in; if they are not spelled on the way out, a working `Ctrl+Alt+S` displays as
`Ctrl+Alt+0x53` and that literal gets written to `config.toml` on the next edit.

**`SW_HIDE` does not hide the Windows 11 taskbar.** The shell re-shows it, sometimes
within the same frame, so the shell logged "Taskbars hidden" while leaving a fully visible
taskbar. `SetWindowPos` with `SWP_HIDEWINDOW` plus parking the window off-screen is what
works.

**Anything that hides the taskbar must be able to put it back.** `ForceRestoreTaskbar`,
which the watchdog calls after a crash, originally only called `ShowWindow`. With the
off-screen parking that would have restored the taskbar to *visible but still at
-32000,-32000*: no taskbar, no desktop, and a rescue that reported success. It now moves
the window back before showing it.

**Tests must not touch the real `%APPDATA%`.** `NewServer` defaults its discovery
descriptor to `%APPDATA%\NovaBar\ipc.json`. A test that calls `Start` writes the user's
live discovery file and `Stop` then deletes it — and that descriptor is how `shellctl` and
every widget find a running daemon, so running the suite against a live shell breaks its
IPC discovery. `Server.SetDescriptorPath` redirects it; `readDescriptorAt` reads a
specific one. Four tests were reading the global path and only ever passed because both
locations coincided.

**A manifest is a request, not a grant.** The daemon enforces permissions server-side, and
the widget's declared set is read from its own `manifest.json`. A hard-coded list at the
call site drifted out of step with the launcher widget's manifest and silently broke
`pins.toggle` and `window.focus` for every user. Derive it instead.

**`Get-Content *.go -Raw` in PowerShell concatenates every file in the package** and
writes them into one. It destroyed `internal/win` once. Restore with `git checkout` and
use the edit tool.

**Config defaults do not reach an existing file.** `Load` reads what is on disk, so
changing a default only affects fresh installs. A user upgrading keeps the old value
until it is changed explicitly, in the file or through the settings UI.

---

## The permission model

One permission per RPC method, enforced server-side. A method with no entry is not
callable by widgets at all, so the safe default for a new method is to be inaccessible
until someone decides what it should require.

| Permission | Grants |
| --- | --- |
| `windows.read` | Observing tracked windows |
| `windows.focus` | Bringing a window to the front |
| `windows.control` | Moving and closing windows |
| `launcher.use` | Searching and launching applications |
| `pins.manage` | Changing the dock's pinned applications |
| `audio.read` | Observing per-application audio sessions |
| `audio.control` | Changing volume or mute |
| `media.control` | Driving media transport |
| `config.read` | Reading the configuration |
| `config.write` | Changing the configuration |

`windows.focus` is split from `windows.control` on purpose: focusing is what nearly every
widget wants and is almost harmless, while moving and closing a window the user did not
target is not. The same reasoning separates `audio.read` from `audio.control` and
`launcher.use` from `pins.manage`.

`general.hide_taskbar` and `hotkeys.rescue` are marked `SafetyProtect`. The daemon
refuses to change them while the rescue hotkey is not bound and working, because those
are the settings that get the user out.

---

## Layout of the tree

```
cmd/daemon        the shell: wiring, surfaces, the message pump
cmd/shellctl      the control CLI
internal/win      Win32: taskbar, hotkeys, work area, icons
internal/rpc      JSON-RPC over a loopback WebSocket, permissions, pub/sub
internal/config   config file, validation, and the schema the settings UI is built from
internal/pins     the dock's pin store
internal/volume   per-application volume via Core Audio
internal/media    GSMTC bridge: activation works, reads do not
internal/tiling   the pure-function tiling engine
internal/webview  WebView2 surfaces hosted in the shell's own windows
internal/overlay  transient surfaces that reserve no work area
widgets/          bar, dock, launcher, settings
branding/         the icon and wordmark, and how to regenerate them
docs/             the M4 WebView2 findings and the M5 GSMTC findings
```

`docs/gsmtc-m5-findings.md` holds the verified GSMTC ABI, including the vtable slots and
the two bugs that cost the most time. `docs/webview2-m4-findings.md` has the WebView2
equivalents.

---

## Working agreements on this project

- **No cgo, no UAC.** `build.ps1 verify` asserts the tree builds with `CGO_ENABLED=0`.
- **Safety first.** Never strand the user without a desktop. The rescue hotkey and the
  watchdog are the two things that must always work.
- **Do not fabricate data.** A surface that cannot read a real value reports that it
  cannot, rather than inventing a plausible one. Several widgets use a monogram because
  real icon extraction was not wired up; the same rule applies everywhere else.
- **The settings UI is generated from the schema**, so it cannot drift from the config it
  edits.
- **Tests must not depend on the developer's machine.** No real `%APPDATA%`, no real
  network, no real user session state.
