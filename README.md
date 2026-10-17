# NovaBar

A lightweight desktop shell and tiling window manager for Windows 10 and 11, written
in pure Go. No cgo, no UAC elevation, single binary.

## Status

Milestone 1.5 (Safety & Correctness) complete. See [Roadmap](#roadmap).

The daemon, tiling engine, IPC, config, launcher and CLI work. The WebView2 UI
surfaces (bar, dock, launcher overlay) are **not** built yet — the bar currently
reserves its space with a plain Win32 AppBar window and nothing is drawn in it.

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

If the config cannot be parsed at startup, NovaBar runs **inert** (no AppBar, no
tiling) rather than falling back to a fully featured shell. Unreadable config is not
permission to take over the screen.

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
internal/bar      AppBar registration and work-area reservation
internal/workspace virtual workspaces over window visibility
internal/config   TOML schema, validation, live-reload watcher
internal/rpc      token-authenticated JSON-RPC over loopback WebSocket + control endpoint
internal/launcher Start Menu + PATH indexing, ranked search, validated launch
internal/hotkey   global hotkey registration
internal/watchdog detached recovery process
internal/media    GSMTC seam (bridge not implemented; reports unavailable honestly)
```

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
| M3 AppBar, workspaces, taskbar management | Done |
| M4 WebView2 UI, widget platform | Not started — see open risk below |
| M5 App launcher, GSMTC media | Partial (launcher done; media bridge not implemented) |

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