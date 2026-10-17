# M4 findings: WebView2 in pure Go

Settles the open risk from the M1.5 README, and records what `ngbcoder/SeelenNovaBar`
(our Seelen UI fork) does that is worth copying.

## 1. The no-cgo constraint holds

**`github.com/jchv/go-webview2` is pure Go and works with `CGO_ENABLED=0`.**

Verified empirically, not assumed:

```
go get github.com/jchv/go-webview2@v0.0.0-20260205173254-56598839c808
grep 'import "C"' -r <module>     -> no matches
grep 'go:build.*cgo'   -r <module> -> no matches
CGO_ENABLED=0 go build             -> exit 0, links, runs
```

Its only dependencies are `jchv/go-winloader` (dynamic DLL resolution) and
`golang.org/x/sys`.

### What actually broke the old NovaBar

The previous NovaBar used **Wails v3**, which requires cgo and drags in `go-ole`.
Its `feat/m1-keep-awake` branch had already begun migrating to `jchv/go-webview2`
alongside systray. Wails was the problem, not WebView2. This is worth remembering
before anyone reaches for a framework.

### API shape that matters

```go
w := webview2.New(false)                  // or NewWithOptions
w.Create(debug, existingHWND)             // attach to a window we already own
w.Bind("nova", handler)                   // Go <- JS
w.Init(js)                                // JS -> Go
w.Dispatch(func(){...})                   // thread-safe UI mutation
```

`Create(debug bool, window unsafe.Pointer)` is the important one: it attaches to an
**existing HWND**. The AppBar window in `internal/bar` already exists and is already
registered with the shell, so WebView2 can be hosted inside it rather than replacing
it. That keeps the work-area reservation logic where it is and isolates WebView2 to a
new package.

---

## 2. What Seelen does that we should copy

Seelen is Rust + Tauri + Angular, so none of its code ports. The techniques do.

### Window flags (from `src/background/widgets/webview.rs`)

| Tauri | Ours | Notes |
| --- | --- | --- |
| `.transparent(true)` | `WS_EX_LAYERED` + DWM | needed |
| `.decorations(false)` | `WS_POPUP` | already have |
| `.shadow(false)` | compensate in tiling | already have |
| `.skip_taskbar(true)` | `WS_EX_TOOLWINDOW` | already have |
| `.focusable(false)` | `WS_EX_NOACTIVATE` | **not yet applied — see below** |
| `.always_on_top(true)` | `HWND_TOPMOST` | already have |

Seelen builds desktop widgets with `.focusable(false).focused(false)` so clicking the
bar never pulls focus away from the app underneath.

**Gap in our code:** `WS_EX_NOACTIVATE` is defined at `internal/win/win32.go` but never
set on the AppBar. The current bar steals focus when clicked. One constant to add to the
`CreateWindowEx` ex-style in `internal/bar/appbar.go`.

### Chromium flags — copy these verbatim

Seelen's `BASE_ARGS`. The important ones for a desktop shell:

```
--disable-background-timer-throttling
--disable-backgrounding-occluded-windows
--disable-features=...PaintHolding,CalculateNativeWinOcclusion,...
--disk-cache-size=0
--disable-application-cache
--disable-background-networking
--disable-component-update
--disable-sync
--no-pings
--disable-breakpad
--disable-crash-reporter
```

**The two timer/occlusion flags are not optional.** A bar window is never focused and
often counts as occluded. Without them Chromium throttles background timers to roughly
one tick per second and the clock, animations and any polling widget visibly stutter.
This is the single most common way a WebView2 desktop bar ships broken.

`--disable-background-networking` and friends keep a widget surface from making any
outbound connections at all.

They also pass a GPU arg set (`GPU_ARGS`) toggled by a hardware-acceleration setting;
worth mirroring once we have a preference for it.

### Disable system animations

```rust
WindowsApi::set_system_transitions_disabled(hwnd, true)
```

Seelen turns off minimize/restore transitions on every widget, because widgets animate
their own show/hide and the OS animation on top of it causes visible flicker. Our bar
is toggled by hotkey and would flicker the same way. Needs `SetWindowLong(GWL_STYLE)` with
`WS_EX_NOACTIVATE`-adjacent flags plus `SPI_SETANIMATION` scoped per-window, or
`AnimateWindow` suppression on toggle.

### AppBar registration

Seelen's `src/background/windows_api/app_bar.rs` uses the same sequence we do —
`ABM_NEW`, `ABM_SETPOS`, `ABM_REMOVE`, with `cbSize` and `uEdge` set. Two improvements:

- It keeps a **global registry of registered bars** and skips re-registration. We only
  guard with a per-instance bool, which is fine for one bar but will not survive
  multiple bar instances (top + bottom, or per-monitor).
- It calls **`ABM_SETSTATE`** with `ABS_ALWAYSONTOP` / `ABS_AUTOHIDE` / `ABS_AUTOHIDEBAR`.
  We declare `ABM_SETSTATE` and never use it. `ABS_ALWAYSONTOP` is what keeps our bar
  above other always-on-top windows.

### Transparency: do NOT do Win32 hit-testing

A search for `WM_NCHITTEST` and `HTTRANSPARENT` across all of Seelen returns **nothing**.
They get click-through behaviour with CSS instead:

```css
/* global.css */      pointer-events: none;
/* CanvasRenderer */  pointer-events: auto;   /* only on real controls */
```

So: `pointer-events: none` on the root, re-enabled on interactive elements. Much simpler
and more robust than intercepting `WM_NCHITTEST`.

For the AppBar specifically this is sufficient and no hit-testing is needed at all — the
bar occupies a strip the AppBar reservation has already removed from every window's work
area, so there is nothing underneath to click through to.

Click-through *does* matter later for a floating overlay or desktop widget. Treat that as
a separate problem for when such a widget exists, not as an M4 blocker.

---

## 3. Revised M4 scope

1. ~~Add `WS_EX_NOACTIVATE` to the AppBar~~ — done.
2. ~~Add `ABM_SETSTATE` + `ABS_ALWAYSONTOP` to AppBar registration~~ — done.
3. New `internal/webview`: wrap `jchv/go-webview2`, host inside an existing bar HWND.
4. Pass Seelen's `BASE_ARGS` via `additional_browser_args` (or the equivalent).
5. Disable system transitions on the host window.
6. Bind the existing `sdk/novabar.ts` surface over `Bind`/`Init`, driven by the
   already-working JSON-RPC layer and its topics.
7. `widgets/bar/index.html` gets `pointer-events` handling.
8. The launcher overlay and dock become bars of their own on their own edges, configured
   through the `[[bars]]` array that already exists.

`internal/win`, `internal/tiling` and `internal/rpc` stay pure Go and cgo-free; only
`internal/webview` gains the WebView2 dependency, and `build.ps1 verify` keeps asserting
`CGO_ENABLED=0` across the whole tree so a regression there is caught immediately.

## 4. Resolved since this was written

- **Runtime availability: closed.** NovaBar targets Windows 11 only, which ships the
  WebView2 runtime. No `WebView2Loader.dll` needs bundling and no graceful-failure path
  is required. The daemon warns at startup on a build below 22000 rather than refusing
  to start, since an unsupported build may still work and a hard failure on a shell
  would be worse than the risk.
- **Multiple AppBar instances: done.** `internal/bar` now has a `Manager` that
  reconciles any number of bars against config, one per screen edge, with add/remove/
  resize applied live on config save. It uses the registration-registry pattern
  described above, so `ABM_NEW` is never sent twice for one HWND. Bars are configured as
  a `[[bars]]` array rather than the old single `[bar]` table, and `hide_taskbar` moved
  to `[general]` because it is one action on one shell object, not a per-bar property.

## 5. Still open

- **Per-monitor bars.** AppBars currently reserve against the primary screen metrics
  only (`GetSystemMetrics`). On a multi-monitor setup a bar reserved per screen needs
  the non-primary monitor's `MONITORINFO`, and the work-area interaction between bars on
  different displays is untested here.
- **Left/right bars and the work area.** Only top and bottom were exercised live. A
  left bar shrinks the work area horizontally, which interacts with any app that assumes
  a full-width taskbar; worth a manual check before enabling one.