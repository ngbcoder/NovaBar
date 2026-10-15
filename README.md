# NovaBar+

NovaBar+ is a lightweight, customizable desktop shell and tiling window manager for Windows 10 and 11 written in pure Go (no cgo required, no UAC elevation needed).

## Features
- **Pure-Go Core**: Single binary daemon without cgo or admin rights.
- **Tiling Engine**: BSP, columns, rows, and monocle layouts with gaps and DWM border compensation.
- **Safety by Design**: Native taskbar is never trapped; emergency restore via `shellctl --rescue` or global hotkey.
- **Built-in Onboarding**: Friendly first-run experience introducing safety features and keyboard shortcuts.
- **Extensible UI**: Web-tech driven widget surfaces and AppBar overlays via WebView2 and JSON-RPC.

## Building & Running

### Requirements
- Go 1.22+ on Windows 10/11
- Standard user permissions (no Administrator/UAC required)

### Build
```powershell
go build -o bin/novabar-daemon.exe ./cmd/daemon
go build -o bin/shellctl.exe ./cmd/shellctl
```

### Run Tests
```powershell
go test -v ./...
```

### Safety Rescue
If you ever want to immediately unhide and restore the native Windows taskbar:
```powershell
.\bin\shellctl.exe --rescue
```
