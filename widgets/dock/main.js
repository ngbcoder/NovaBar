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

function makeTile({ label, glyph, running: isRunning, active, onClick, onContextMenu }) {
    const btn = document.createElement('button');
    btn.className = 'tile' + (isRunning ? ' running' : '') + (active ? ' active' : '');
    btn.textContent = glyph;

    const tip = document.createElement('span');
    tip.className = 'tile-label';
    tip.textContent = label;
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
    return btn;
}

function separator() {
    const sep = document.createElement('div');
    sep.className = 'sep';
    return sep;
}

function render() {
    const tiles = [];

    for (const app of pinned) {
        tiles.push(makeTile({
            label: app.name,
            glyph: monogram(app.name),
            running: running.some((w) => w.proc_name === app.proc_name),
            active: running.some((w) => w.proc_name === app.proc_name && w.hwnd === activeHwnd),
            onClick: () => launch(app.target),
            onContextMenu: () => unpin(app),
        }));
    }

    if (pinned.length > 0 && running.length > 0) tiles.push(separator());

    for (const win of running) {
        if (pinned.some((app) => app.proc_name === win.proc_name)) continue;
        tiles.push(makeTile({
            label: nameFor(win),
            glyph: monogram(win.proc_name || win.title),
            running: true,
            active: win.hwnd === activeHwnd,
            onClick: () => focus(win.hwnd),
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
    const next = [
        pinned.map((a) => a.target).join(','),
        running.map((w) => `${w.hwnd}:${w.title}`).join(','),
        String(activeHwnd),
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
}

main();