# Getting Started

## Requirements

- Windows 11 (build 22000 or newer).
- Go 1.22+ to build. No other dependencies.

## Build

```powershell
.\build.ps1            # build bin/novabar.exe and bin/shellctl.exe
.\build.ps1 test       # build, then run the test suite
.\build.ps1 verify     # build with CGO_ENABLED=0, vet, and test
.\build.ps1 clean
```

`verify` is what CI should run. It asserts the pure-Go constraint.

## Run

```powershell
.\bin\novabar.exe
```

Config lives at `%APPDATA%\NovaBar\config.toml` and is created with safe defaults on
first run. It is watched live; saving the file re-applies the settings without a
restart.

If the config cannot be parsed at startup, NovaBar runs **inert** (no bars, no tiling)
rather than taking over the screen.

## First-run safety

`hide_taskbar` defaults to `false`. Enabling it is an explicit opt-in, because it is
the one setting the watchdog exists to recover from. The rescue hotkey
(`Ctrl+Alt+Shift+R` by default) is registered during startup, before anything can block.

## Recovery

```powershell
.\bin\shellctl.exe rescue     # works with or without a running daemon
```

See [Safety](safety.md) for the full recovery model.