// The NovaBar dock widget.
//
// Shows pinned applications followed by currently running windows. Pinned entries come
// from the daemon's pin store, which is what makes them outlive the session; the running
// list is live and unsaved because it is a reflection of reality rather than a choice.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;
const dock = document.getElementById('dock');

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Applications the user has pinned, in dock order. */
let pinned = [];

/** Windows last seen, keyed by hwnd. */
let running = [];

/**
 * Per-application audio volume, keyed by pid.
 *
 * Windows refuses volume changes for some application types, most notably Store apps on
 * recent builds. The daemon reports that as an error and the widget logs it rather than
 * pretending the change took, so the bar is only drawn where volume is actually known.
 */
let audio = new Map();

/** Whether the daemon has an audio output device at all. */
let audioAvailable = false;

/** Handle the shell believes is focused, if the daemon ever tells us. */
let activeHwnd = 0;

/**
 * Icon for an app.
 *
 * Real icon extraction is not wired up, so this derives a stable two-letter monogram
 * instead. Deterministic on purpose: an icon that changed on every update would make the
 * dock impossible to scan.
 */
function monogram(name) {
    const words = name.trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '?';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
}

function makeTile({ label, glyph, running: isRunning, active, onClick, onContextMenu, onWheel, volume }) {
    const btn = document.createElement('button');
    btn.className = 'tile' + (isRunning ? ' running' : '') + (active ? ' active' : '');
    btn.textContent = glyph;

    const tip = document.createElement('span');
    tip.className = 'tile-label';
    tip.textContent = label;
    btn.appendChild(tip);

    // Volume indicator. Only drawn when the level is known, so a missing session is
    // visibly absent rather than shown as a misleading zero.
    if (volume !== null && volume !== undefined) {
        const bar = document.createElement('span');
        bar.className = 'tile-volume' + (volume.muted ? ' muted' : '');
        const fill = document.createElement('span');
        fill.className = 'tile-volume-fill';
        // Muted sessions keep their level but must not read as audible.
        fill.style.width = `${Math.round((volume.muted ? 0 : volume.volume) * 100)}%`;
        bar.appendChild(fill);
        btn.appendChild(bar);
        btn.title = `${label}${volume.muted ? ' (muted)' : ` — ${Math.round(volume.volume * 100)}%`}`;
    }

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
        // Wheel adjusts volume. preventDefault because otherwise the page scrolls behind
        // the dock, which is not scrollable and looks like a glitch.
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
 * name is the best available answer and is exact for the common case of one application
 * having one audio session.
 */
function volumeForProcName(procName) {
    if (!procName) return null;
    for (const s of audio.values()) {
        if (s.proc_name === procName) return s;
    }
    return null;
}

/**
 * Nudge one application's volume.
 *
 * The daemon clamps and validates; the widget keeps its own copy consistent so the bar
 * moves immediately rather than after the next poll.
 */
async function adjustVolume(pid, delta) {
    const current = audio.get(pid);
    if (!current) return;
    const next = Math.min(1, Math.max(0, current.volume + delta));
    try {
        await sdk.call('volume.set', { pid, level: next });
        // Optimistic update; the audio.updated broadcast corrects it if refused.
        audio.set(pid, { ...current, volume: next, muted: next === 0 ? current.muted : false });
        renderIfChanged();
    } catch (err) {
        console.error('volume change refused', err);
    }
}

function render() {
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

    if (tiles.length === 0) {
        const empty = document.createElement('div');
        empty.style.cssText = 'color:#9ca3af;font-size:11px;text-align:center;padding:14px 6px;line-height:1.5';
        empty.textContent = 'No open windows';
        tiles.push(empty);
    }

    dock.replaceChildren(...tiles);
}

/** Re-render only when something actually changed, so hover state survives updates. */
let signature = '';
function renderIfChanged() {
    // Volume levels are part of the signature, otherwise a wheel adjustment would not
    // repaint until some unrelated change came along.
    const vol = [...audio.entries()].map(([pid, v]) => `${pid}:${v.muted ? 'm' : v.volume.toFixed(2)}`).join(',');
    const next = [
        pinned.map((a) => a.target).join(','),
        running.map((w) => `${w.hwnd}:${w.title}`).join(','),
        String(activeHwnd),
        vol,
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
    // The daemon owns the store; this widget only reads it. An older version seeded
    // pins from the whole application index, which made the dock a wall of every
    // installed program and told the user nothing about what they actually ran.
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

/** Fallback glyph for a window whose process name is empty. */
function nameFor(win) {
    return win.title || win.proc_name || 'Window';
}

function setRunning(windows) {
    running = windows || [];
    renderIfChanged();
}

/**
 * Replace the audio map from a daemon payload.
 *
 * audioAvailable is kept separately so the dock can distinguish "no output device" from
 * "nothing is playing", which look identical in the session list but mean different
 * things to the user.
 */
function setAudio(payload) {
    if (!payload) {
        audioAvailable = false;
        audio = new Map();
        renderIfChanged();
        return;
    }
    audioAvailable = Boolean(payload.available);
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
        // No audio, or no permission. Either way there is nothing to draw, and the dock
        // stays a dock.
        console.warn('audio unavailable', err);
        setAudio(null);
    }
}

async function main() {
    if (!boot || !boot.hasBridge) {
        dock.textContent = '';
        return;
    }

    try {
        await sdk.connect();
    } catch (err) {
        console.error('could not reach the daemon', err);
        return;
    }

    sdk.subscribe('windows.updated', setRunning);

    // The launcher can pin and unpin while the dock is open, so take the list from the
    // daemon rather than only reading it once at startup.
    sdk.subscribe('pins.changed', (state) => {
        pinned = (state && state.pins) || [];
        renderIfChanged();
    });

    await loadPinned();
    setRunning(await sdk.call('windows.get'));

    await loadAudio();

    // The daemon re-reads audio on an interval and pushes only genuine changes, so the
    // widget does not poll and a volume change from anywhere is picked up here.
    sdk.subscribe('audio.updated', setAudio);
}

main();