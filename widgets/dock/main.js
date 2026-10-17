// The NovaBar dock widget.
//
// Shows pinned applications followed by currently running windows. Pinned entries come
// from the daemon's launcher index, so there is no separate pinning store to keep in
// sync with reality.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;
const dock = document.getElementById('dock');

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Applications the user has pinned, by lowercase process name. */
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

function makeTile({ label, glyph, running: isRunning, active, onClick }) {
    const btn = document.createElement('button');
    btn.className = 'tile' + (isRunning ? ' running' : '') + (active ? ' active' : '');
    btn.textContent = glyph;

    const tip = document.createElement('span');
    tip.className = 'tile-label';
    tip.textContent = label;
    btn.appendChild(tip);

    btn.addEventListener('click', onClick);
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
    // There is no pinning store yet, so nothing is pinned by default.
    //
    // An earlier version seeded this from the whole application index, which made the
    // dock a wall of every installed program and told the user nothing about what was
    // actually running. Showing only running windows is honest and useful; pinning is a
    // real feature that needs a real store rather than an empty list pretending
    // otherwise.
    pinned = [];
}

/** The daemon reports a target path; the widget needs a process name to group by. */
function processNameOf(item) {
    const parts = String(item.target || '').split(/[\\/]/);
    const last = parts[parts.length - 1] || item.name || '';
    return last.toLowerCase().replace(/\.exe$/i, '');
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

    await loadPinned();
    setRunning(await sdk.call('windows.get'));
}

main();