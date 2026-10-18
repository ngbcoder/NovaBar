// The NovaBar floating dock.
//
// A macOS-style dock: a translucent rounded pill floating over the wallpaper, with
// magnification on hover, running dots, and a separator between pinned applications and
// running windows. The window itself spans the monitor and reserves no work area, so
// windows underneath stay fully usable.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;
const dock = document.getElementById('dock');

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Pinned applications, in dock order. */
let pinned = [];

/** Windows last seen. */
let running = [];

/** Handle the shell believes is focused. */
let activeHwnd = 0;

/** Per-application audio volume, keyed by pid. */
let audio = new Map();

/** Icon size in pixels, driven by config.schema. */
let iconSize = 56;

/** How far a tile grows at full magnification, and how wide the falloff is. */
const MAGNIFY_MAX = 1.55;
const MAGNIFY_SPAN = 2; // tiles either side that are affected at all

/**
 * Icon for an app.
 *
 * Real icon extraction is not wired up, so this derives a stable two-letter monogram.
 * Deterministic on purpose: an icon that changed on every update would make the dock
 * impossible to scan.
 */
function monogram(name) {
    const words = String(name || '').trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '?';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
}

function makeTile({ label, glyph, running: isRunning, active, onClick, onContextMenu, onWheel, volume }) {
    const btn = document.createElement('button');
    btn.className = 'tile' + (active ? ' active' : '');

    const g = document.createElement('div');
    g.className = 'glyph';
    g.textContent = glyph;
    btn.appendChild(g);

    if (isRunning) {
        const dot = document.createElement('span');
        dot.className = 'dot';
        btn.appendChild(dot);
    }

    if (volume !== null && volume !== undefined) {
        const bar = document.createElement('span');
        bar.className = 'tile-volume' + (volume.muted ? ' muted' : '');
        const fill = document.createElement('span');
        fill.className = 'tile-volume-fill';
        // A muted session keeps its level but must not read as audible.
        fill.style.width = `${Math.round((volume.muted ? 0 : volume.volume) * 100)}%`;
        bar.appendChild(fill);
        btn.appendChild(bar);
    }

    const tip = document.createElement('span');
    tip.className = 'tile-label';
    tip.textContent = volume && volume.muted ? `${label} (muted)` : label;
    btn.appendChild(tip);

    btn.addEventListener('click', onClick);

    if (onContextMenu) {
        btn.addEventListener('contextmenu', (e) => {
            // Without this the browser menu appears over the dock and the shell looks
            // broken.
            e.preventDefault();
            onContextMenu();
        });
    }
    if (onWheel) {
        btn.addEventListener('wheel', (e) => {
            e.preventDefault();
            onWheel(e.deltaY < 0 ? 0.05 : -0.05);
        }, { passive: false });
    }
    return btn;
}

function separator() {
    const sep = document.createElement('div');
    sep.className = 'sep';
    return sep;
}

/** Volume for a running window, or null when the application has no audio session. */
function volumeForPid(pid) {
    return pid ? (audio.get(pid) ?? null) : null;
}

/**
 * Volume for a pinned tile.
 *
 * A pin is a launch target, not a window, so there is no pid. Matching on the executable
 * name is exact for the common case of one application having one audio session.
 */
function volumeForProcName(procName) {
    if (!procName) return null;
    for (const s of audio.values()) {
        if (s.proc_name === procName) return s;
    }
    return null;
}

/** Nudge one application's volume. */
async function adjustVolume(pid, delta) {
    const current = audio.get(pid);
    if (!current) return;
    const next = Math.min(1, Math.max(0, current.volume + delta));
    try {
        await sdk.call('volume.set', { pid, level: next });
        // Optimistic; the audio.updated broadcast corrects it if Windows refused.
        audio.set(pid, { ...current, volume: next, muted: next === 0 ? current.muted : false });
        renderIfChanged();
    } catch (err) {
        console.error('volume change refused', err);
    }
}

/** Build the ordered tile list. */
function buildTiles() {
    const tiles = [];

    for (const app of pinned) {
        const vol = volumeForProcName(app.proc_name);
        tiles.push(makeTile({
            label: app.name,
            glyph: monogram(app.name),
            running: running.some((w) => w.proc_name === app.proc_name),
            active: running.some((w) => w.proc_name === app.proc_name && w.hwnd === activeHwnd),
            onClick: () => launch(app.target),
            onContextMenu: () => unpin(app),
            // Without a pid there is nothing to address, so the wheel is left unbound
            // rather than silently adjusting some other application.
            onWheel: vol ? (d) => adjustVolume(vol.pid, d) : undefined,
            volume: vol,
        }));
    }

    if (pinned.length > 0 && running.length > 0) tiles.push(separator());

    for (const win of running) {
        if (pinned.some((app) => app.proc_name === win.proc_name)) continue;
        const vol = volumeForPid(win.pid);
        tiles.push(makeTile({
            label: nameFor(win),
            glyph: monogram(win.proc_name || win.title),
            running: true,
            active: win.hwnd === activeHwnd,
            onClick: () => focus(win.hwnd),
            onWheel: vol ? (d) => adjustVolume(vol.pid, d) : undefined,
            volume: vol,
        }));
    }

    return tiles;
}

function render() {
    const tiles = buildTiles();

    if (tiles.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = 'Nothing open';
        tiles.push(empty);
    }

    dock.replaceChildren(...tiles);
    wireMagnification();
}

/**
 * macOS-style magnification.
 *
 * The pointer's horizontal distance from each tile's centre decides its scale, with a
 * cosine falloff over MAGNIFY_SPAN tiles either side. Reimplemented on mousemove rather
 * than relying on CSS :hover because the real effect scales *neighbours* too, which CSS
 * cannot express.
 */
function wireMagnification() {
    const tiles = [...dock.querySelectorAll('.tile')];
    if (tiles.length === 0) return;

    const reset = () => tiles.forEach((t) => { t.style.transform = ''; });

    dock.onpointermove = (e) => {
        for (const tile of tiles) {
            const r = tile.getBoundingClientRect();
            const centre = r.left + r.width / 2;
            const step = r.width + 4;

            // Distance in tile-widths, so the falloff is independent of icon size.
            const distance = Math.abs(e.clientX - centre) / step;
            if (distance > MAGNIFY_SPAN) {
                tile.style.transform = '';
                continue;
            }
            const falloff = Math.cos((distance / MAGNIFY_SPAN) * (Math.PI / 2));
            tile.style.transform = `scale(${1 + (MAGNIFY_MAX - 1) * falloff})`;
        }
    };

    dock.onpointerleave = reset;
}

let signature = '';
function renderIfChanged() {
    // Volume levels are part of the signature, otherwise a wheel adjustment would not
    // repaint until some unrelated change came along.
    const vol = [...audio.entries()]
        .map(([pid, v]) => `${pid}:${v.muted ? 'm' : v.volume.toFixed(2)}`).join(',');
    const next = [
        pinned.map((a) => a.target).join(','),
        running.map((w) => `${w.hwnd}:${w.title}`).join(','),
        String(activeHwnd),
        vol,
        String(iconSize),
    ].join('|');

    if (next === signature) return;
    signature = next;
    render();
}

async function launch(target) {
    try {
        await sdk.call('launcher.launch', { target });
    } catch (err) {
        console.error('launch failed', err);
    }
}

async function focus(hwnd) {
    try {
        await sdk.call('window.focus', { hwnd });
    } catch (err) {
        console.error('focus failed', err);
    }
}

async function loadPinned() {
    try {
        const res = await sdk.call('pins.list');
        pinned = (res && res.pins) || [];
    } catch (err) {
        // Showing running windows only is a usable dock, so a failure here degrades to
        // that rather than leaving an error on screen.
        console.error('could not load pins', err);
        pinned = [];
    }
}

/** Unpin a tile. Right-click, because left-click launches. */
async function unpin(app) {
    try {
        await sdk.call('pins.remove', { target: app.target });
        pinned = pinned.filter((p) => p.target !== app.target);
        renderIfChanged();
    } catch (err) {
        console.error('unpin failed', err);
    }
}

/** Fallback label for a window whose process name is empty. */
function nameFor(win) {
    return win.title || win.proc_name || 'Window';
}

function setRunning(windows) {
    running = windows || [];
    renderIfChanged();
}

/** Replace the audio map from a daemon payload. */
function setAudio(payload) {
    if (!payload) {
        audio = new Map();
        renderIfChanged();
        return;
    }
    const next = new Map();
    for (const s of payload.sessions || []) {
        next.set(s.pid, {
            pid: s.pid,
            proc_name: (s.proc_name || '').toLowerCase(),
            label: s.display_name || s.proc_name || `PID ${s.pid}`,
            volume: s.volume,
            muted: s.muted,
        });
    }
    audio = next;
    renderIfChanged();
}

async function loadAudio() {
    try {
        setAudio(await sdk.call('volume.list'));
    } catch (err) {
        console.warn('audio unavailable', err);
        setAudio(null);
    }
}

/** Apply the daemon's dock size. */
function setIconSize(size) {
    const n = Number(size);
    if (!Number.isFinite(n) || n <= 0) return;
    iconSize = n;
    document.documentElement.style.setProperty('--icon', `${n}px`);
}

async function main() {
    if (!boot || !boot.hasBridge) {
        dock.remove();
        return;
    }

    try {
        await sdk.connect();
    } catch (err) {
        console.error('could not reach the daemon', err);
        dock.remove();
        return;
    }

    // The dock is opened by the daemon, never by the user, so there is nothing to
    // dismiss and Escape must not close it.
    document.addEventListener('keydown', (e) => e.preventDefault());

    sdk.subscribe('windows.updated', setRunning);
    sdk.subscribe('audio.updated', setAudio);
    // Size and margin changes arrive here rather than by rebuilding the surface, so a
    // slider drag does not tear down and recreate a WebView2 controller on every step.
    sdk.subscribe('system.stats', (stats) => {
        if (stats && stats.dock) setIconSize(stats.dock.size);
    });

    await loadPinned();
    setRunning(await sdk.call('windows.get'));
    await loadAudio();

    // The schema is the source for the icon size, the same as the settings page. Reading
    // it here means adding dock settings to config.toml is enough to make them apply.
    try {
        const schema = await sdk.call('config.schema');
        if (schema && schema.values) setIconSize(schema.values['dock.size']);
    } catch (err) {
        console.warn('could not read the dock size from the schema', err);
    }
}

main();