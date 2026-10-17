# NovaBar

A lightweight desktop shell and tiling window manager for **Windows 11**, written in
pure Go. No cgo, no UAC elevation, single binary.

## Requirements

Windows 11 (build 22000 or newer).

This is a deliberate scope decision. Windows 11 guarantees the WebView2 runtime is
present, which means the M4 UI layer needs no bundled `WebView2Loader.dll` and no
fallback path. On startup the daemon logs a clear warning if it detects an older build,
rather than failing mysteriously later.

Go 1.22+ to build. No other dependencies.

## Status

Milestones 1–4 are complete. Milestone 5 is partial.

Three surfaces are live, all WebView2 hosted inside the shell's own windows and all
talking to the daemon over authenticated, permission-checked JSON-RPC:

| Surface | Kind | Widget |
| --- | --- | --- |
| Bar | AppBar, reserves work area | `widgets/bar` |
| Dock | AppBar, reserves work area | `widgets/dock` |
| Launcher | Overlay, reserves nothing | `widgets/launcher` |

Verified together on a two-bar layout: both surfaces attach, connect, receive only the
permissions their manifests declare, and subscribe to their topics.

Still to do: the GSMTC media bridge (the bar's transport controls work but the media area
stays hidden until real media data exists) and app pinning for the dock, which currently
shows only running windows.

## Safety

NovaBar's central promise is that it cannot strand you without a desktop. Four
independent mechanisms enforce it:

| Mechanism | Trigger | Notes |
| --- | --- | --- |
| Watchdog sub-process | Daemon killed, crashed, or power-managed | Waits on the daemon's process handle, restores the taskbars on exit |
| Rescue hotkey | `Ctrl+Alt+Shift+R` (configurable) | Registered during startup, before anything can block |
| `shellctl rescue` | Manual | Talks to the daemon so the AppBar reservation is also released |
| `defer` cleanup | Clean exit | Also runs on rescue and on Ctrl+C |

The watchdog is started **before** any component that can affect the desktop, and
before the first-run modal. There is no window during startup in which a crash leaves
you without a recovery path.

The taskbar is **not** hidden by default (`bar.hide_taskbar = false`). Enabling it is
an explicit opt-in, because it is the one setting the watchdog exists to recover from.

If the daemon is gone and nothing responds:

```powershell
.\bin\shellctl.exe rescue     # works with or without a running daemon
```

## Build

Requires Go 1.22+ on Windows. No other dependencies.

```powershell
.\build.ps1            # build bin/novabar.exe and bin/shellctl.exe
.\build.ps1 test       # build, then run the test suite
.\build.ps1 verify     # build with CGO_ENABLED=0, vet, and test
.\build.ps1 clean
```

`verify` is what CI should run. It asserts the pure-Go constraint rather than trusting
it, because a cgo dependency arriving through a transitive import would only show up on
a user's machine.

## Run

```powershell
.\bin\novabar.exe
```

Config lives at `%APPDATA%\NovaBar\config.toml` and is created with safe defaults on
first run. It is watched live; saving the file re-applies the settings without a
restart. A malformed edit is reported and the previous config stays in force — a typo
can never take the shell down mid-edit.

If the config cannot be parsed at startup, NovaBar runs **inert** (no bars, no tiling)
rather than falling back to a fully featured shell. Unreadable config is not permission
to take over the screen.

### Bars

Any number of bars may be enabled, **at most one per screen edge**:

```toml
[[bars]]
edge = "top"      # top | bottom | left | right
size = 40         # reserved thickness
enabled = true
name = "main"

[[bars]]
edge = "bottom"
size = 6
enabled = true
name = "status"

[[bars]]
edge = "left"
size = 64
enabled = false   # present but off; flip to true to add a dock
name = "dock"
```

`size` is the vertical extent for top and bottom bars, the horizontal extent for left
and right. Adding, enabling, disabling or resizing a bar takes effect on the next save,
with no restart.

One bar per edge is a hard rule, enforced at load. The AppBar API reserves a single
strip per edge, so two bars on the same edge would each shrink the work area from the
same side and leave a gap twice as large as either bar.

`hide_taskbar` is a single global setting under `[general]`, not per bar — it is one
action on one shell object, so asking two bars to do it would be meaningless.

To hide the native taskbar and draw your own, add:

```toml
[general]
hide_taskbar = true
```

That is the one setting the watchdog exists to recover from.

## CLI

```
shellctl --rescue          restore the taskbar and shut the shell down
shellctl --shutdown        the same, without the emergency framing
shellctl --status          show shell status
shellctl --status --json   machine-readable status
```

`--rescue` asks the running daemon to clean up, because that is the only path that also
releases the reserved AppBar work area and stops the daemon re-hiding the taskbar. With
no daemon reachable it falls back to un-hiding the taskbars directly.

## Architecture

```
cmd/daemon        daemon entrypoint, subsystem wiring, event pump, RPC methods
cmd/shellctl      control CLI
internal/win      Win32 bindings; every callback thunk is hoisted to a package var
internal/tracker  window enumeration and filtering, stable ordering
internal/tiling   pure layout functions, rule evaluator, DWM frame correction
internal/bar      multi-bar AppBar manager: registration, work-area reservation
internal/workspace virtual workspaces over window visibility
internal/config   TOML schema, validation, live-reload watcher
internal/rpc      token-authenticated JSON-RPC over loopback WebSocket + control endpoint
internal/launcher Start Menu + PATH indexing, ranked search, validated launch
internal/hotkey   global hotkey registration
internal/watchdog detached recovery process
internal/webview  WebView2 surfaces hosted in the shell's own windows
internal/overlay  transient surfaces that reserve no work area (the launcher)
internal/media    GSMTC seam (bridge not implemented; reports unavailable honestly)
```

### Widgets

A widget is a directory under `widgets/` with an `index.html` and an optional
`manifest.json`. There is no build step and no bundler: the daemon serves the directory
over loopback and a surface navigates to it.

```toml
[[bars]]
edge = 'top'
size = 40
enabled = true
name = 'main'
widget = 'bar'          # directory under the widgets root
transparent = true      # composite over the desktop
```

The widget root is found next to the executable, then under the working directory, or
set explicitly with `NOVABAR_WIDGETS` when running from a checkout.

The RPC endpoint and session token are **injected** into the page by the daemon before
it loads, as `window.NOVABAR`. They are not written into any file, so a widget on disk
contains no credentials. `sdk/novabar.js` is served from `/sdk` and imported directly.

Widget assets are served without the session token: they are static presentation with
no secrets in them, and the token arrives at runtime instead.

### Widget permissions

A widget's `manifest.json` declares what it needs, and the daemon enforces it on every
call and every subscription:

```json
{ "permissions": ["windows.read", "workspaces.manage", "media.control"] }
```

Available: `windows.read`, `windows.control`, `workspaces.manage`, `launcher.use`,
`media.control`, `config.read`.

Enforcement uses a **per-surface credential**, distinct from the session token. The
session token proves a page belongs to this shell; the credential proves which surface
it is and therefore what that surface's manifest was granted. The session token alone
calls nothing.

An unknown permission in a manifest is rejected and the widget is denied everything,
rather than the entry being silently ignored. A method with no permission mapping is not
callable by widgets at all, so adding one later is inaccessible until someone decides
what it should require.

### Layout geometry

Bar rectangles are computed by the shell, not taken from the system's work area, and
adjacent edges are inset so they never overlap. A horizontal bar keeps the full width and
owns the corner; a vertical bar starts below or beside it. Insetting both, which was the
first attempt, left a dead square belonging to neither bar.

Surfaces track their bar's real rectangle, not its thickness. A left dock is narrow and
full height; sizing it to thickness-squared renders a small square in the top-left
corner, on top of the top bar, which reads as a missing dock rather than as a bug.

### IPC

The RPC server binds an **ephemeral** loopback port and generates a 256-bit session
token, both published to `%APPDATA%\NovaBar\ipc.json` with a user-only ACL. Connections
without the token are refused and pub/sub is opt-in per topic. This matters: an
unauthenticated loopback control surface is reachable from any web page the user
visits.

```jsonc
// widgets connect like this
const sdk = new NovaBarSDK(await fetchConfig());
sdk.connect();
await sdk.call("windows.get");
sdk.subscribe("windows.updated", onWindows);
```

## Roadmap

| Milestone | State |
| --- | --- |
| M1 Win32 core, safety foundations | Done |
| M2 Pure-function tiling engine | Done |
| M3 AppBar, workspaces, taskbar management | Done (multi-bar, multi-edge) |
| M4 WebView2 UI, widget platform | Done (bar, dock, launcher, manifest permissions) |
| M5 App launcher, GSMTC media | Partial (launcher done; media bridge not implemented) |
| M6 Settings menu | Not started — see [Planned: settings menu](#planned-settings-menu) |

### Open risk: WebView2 vs. the no-cgo constraint — **resolved**

WebView2's `ICoreWebView2` is COM, and the obvious pure-Go route appeared blocked
because `go-ole` needs cgo. It is not.

**`github.com/jchv/go-webview2` is pure Go and compiles, links and runs with
`CGO_ENABLED=0`.** Its only dependencies are a DLL loader and `x/sys`.

What actually cost the old NovaBar its no-cgo constraint was **Wails v3**, which
requires cgo. Its `feat/m1-keep-awake` branch had already begun migrating to
`jchv/go-webview2` when it stopped. Wails was the problem, not WebView2 — worth
remembering before reaching for a framework.

`jchv/go-webview2` also exposes `Create(debug bool, window unsafe.Pointer)`, which
attaches to an **existing HWND**. WebView2 can therefore be hosted inside the AppBar
window `internal/bar` already owns, which keeps the work-area reservation logic where it
is and confines the new dependency to one package.

See [docs/webview2-m4-findings.md](docs/webview2-m4-findings.md) for the verification
steps and for what `ngbcoder/SeelenNovaBar` does that is worth copying — window flags,
the Chromium arguments a desktop bar *must* pass, and why the transparency approach is
CSS rather than Win32 hit-testing.

> **Correction to the section above.** The library actually in use is
> `wailsapp/go-webview2`, not `jchv/go-webview2`. Both are pure Go and both build with
> `CGO_ENABLED=0`, but jchv's `pkg/edge` hardcodes a null `environmentOptions` at
> `chromium.go:87`, so Chromium command-line arguments cannot be passed at all — which
> loses the timer-throttling fix described above. `wailsapp/go-webview2` has
> `WithAdditionalBrowserArguments` and attaches to an existing HWND.
>
> Also: jchv's `Create(debug, window)` discards its `window` argument, so it cannot
> attach to an existing window at all. The path that does work is `NewWindow` or
> `NewWithOptions`.

## Planned: settings menu (M6)

Not started. Config is TOML on disk today, which suits a power user and is hostile to
everyone else: no way to discover the available options, no validation feedback, and
enabling a second bar means reading the docs.

An in-shell settings overlay, same shape as the launcher: a plain topmost window, no
work-area reservation, dismissed with the same sentinel.

| Piece | Notes |
| --- | --- |
| Schema introspection | Expose the config schema over RPC so the UI is generated from it. Already partly needed: widget manifests drift from the code that validates them. |
| Typed editing | `config.get` returns the whole document but there is no setter. Needs one, validated server-side, keeping the "inert on error" rule from startup. |
| First toggles | bar enable/edge/size, tiling enabled/layout/gaps, hide taskbar, hotkeys. |
| Hotkey capture | The one thing a text field cannot do. Needs a `RegisterHotKey` conflict check at capture time, which the daemon can do cheaply. |
| Apply without restart | Live reload already covers bar add/remove/edge and taskbar visibility. Layout and gaps do not yet re-tile on change. |
| Safety | Editing `hide_taskbar` and the rescue hotkey edits the things that protect you. Those two should require the rescue path to already be working. |

Not planned: a settings daemon, a background service, or any config persisted outside
`%APPDATA%\NovaBar\config.toml`.