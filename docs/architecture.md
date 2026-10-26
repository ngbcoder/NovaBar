# Architecture

```
cmd/daemon        daemon entrypoint, subsystem wiring, event pump, RPC methods
cmd/shellctl      control CLI
internal/win      Win32 bindings
internal/tracker  window enumeration and filtering
internal/tiling   pure layout functions, rule evaluator, DWM frame correction
internal/bar      multi-bar AppBar manager
internal/workspace virtual workspaces over window visibility
internal/config   TOML schema, validation, live-reload watcher
internal/rpc      token-authenticated JSON-RPC over loopback WebSocket
internal/launcher Start Menu + PATH indexing, ranked search
internal/hotkey   global hotkey registration
internal/watchdog detached recovery process
internal/webview  WebView2 surfaces
internal/overlay  transient surfaces that reserve no work area
internal/media    GSMTC bridge
internal/spotify  Spotify Web API client (PKCE OAuth)
internal/winrt    COM and WinRT ABI slice
internal/winmd    Windows Runtime metadata reader
internal/volume   per-application audio volume (WASAPI)
```

## IPC

The RPC server binds an **ephemeral** loopback port and generates a 256-bit session
token, both published to `%APPDATA%\NovaBar\ipc.json` with a user-only ACL.
Connections without the token are refused.

## Layout geometry

Bar rectangles are computed by the shell. Adjacent edges are inset so they never
overlap. Surfaces track their bar's real rectangle, not its thickness.