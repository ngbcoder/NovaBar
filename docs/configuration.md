# Configuration

Config lives at `%APPDATA%\NovaBar\config.toml`. A malformed edit is reported and the
previous config stays in force.

## General

```toml
[general]
bar_scope = 'primary'   # primary | desktop
first_run = false
log_level = 'info'
rescue_hotkey = 'Ctrl+Alt+Shift+R'
hide_taskbar = false
```

## Bars

Any number of bars, at most one per screen edge:

```toml
[[bars]]
edge = 'top'          # top | bottom | left | right
size = 40
enabled = true
name = 'main'
widget = 'bar'
transparent = true
```

## Tiling

```toml
[tiling]
enabled = true
default_layout = 'bsp'
inner_gap = 8
outer_gap = 12
```

## Dock

```toml
[dock]
enabled = true
size = 56
bottom_margin = 0
```

## Dynamic Island (embedded in the top bar)

```toml
[island]
enabled = true
width = 360
height = 56
show_media = true
show_clock = true
show_date = true
```

The top bar's centre is a persistent pill that always shows the clock and date; when
media is playing it widens and shows the track title and artist, with transport controls
revealed on hover or click.

## Spotify

```toml
[spotify]
enabled = false
client_id = ""
prefer = false
```

Set the redirect URI in the Spotify developer dashboard to
`http://127.0.0.1:8910/spotify/callback`, then connect via the `spotify.connect` RPC
method.

`prefer = true` makes the Spotify Web API the source even when a local GSMTC session
exists. With `prefer = false` the local session wins, and Spotify is used only when
GSMTC has nothing to show. Playback on another device (Spotify Connect) is invisible
to GSMTC, so it appears either way, and the island follows track changes from
whichever source it chose.

## Hotkeys

```toml
[hotkeys]
rescue = 'Ctrl+Alt+Shift+R'
launcher = 'Shift+Space'
workspace_prev = 'Alt+Left'
workspace_next = 'Alt+Right'
settings = 'Ctrl+Alt+S'
```

## Rules

```toml
[[rules]]
process = 'Taskmgr.exe'
class = ''
title = ''
action = 'float'
```

Available actions: `float`, `ignore`. Rules match by process, window class and title.