# Safety

NovaBar's central promise is that it cannot strand you without a desktop. Four
independent mechanisms enforce it:

| Mechanism | Trigger | Notes |
| --- | --- | --- |
| Watchdog sub-process | Daemon killed, crashed, or power-managed | Waits on the daemon's process handle, restores the taskbars on exit |
| Rescue hotkey | `Ctrl+Alt+Shift+R` (configurable) | Registered during startup, before anything can block |
| `shellctl rescue` | Manual | Talks to the daemon so the AppBar reservation is also released |
| `defer` cleanup | Clean exit | Also runs on rescue and on Ctrl+C |

The watchdog is started **before** any component that can affect the desktop, and
before the first-run modal.

The taskbar is **not** hidden by default (`bar.hide_taskbar = false`). Enabling it is
an explicit opt-in.

If the daemon is gone and nothing responds:

```powershell
.\bin\shellctl.exe rescue     # works with or without a running daemon
```

## Safety-protected settings

`hide_taskbar` and the rescue hotkey are marked `SafetyProtect`. The daemon refuses
them while the rescue path is not working.