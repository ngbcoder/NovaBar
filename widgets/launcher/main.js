// The NovaBar launcher overlay.
//
// Searches installed applications and currently running windows, and launches the
// selected one. Dismisses itself by posting the daemon's dismiss sentinel, because an
// overlay that lingers after Escape would sit there covering the desktop.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;

const input = document.getElementById('q');
const list = document.getElementById('list');

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Dismiss sentinel understood by the daemon. */
const DISMISS = '__novabar_dismiss__';

/** Results currently shown. */
let results = [];

/** Launch targets the user has pinned, lowercased for matching. */
let pinnedTargets = new Set();

/** Index of the highlighted row. */
let selected = 0;

/** Debounce handle, so a fast typist does not issue a search per keystroke. */
let debounceTimer = null;

/** Running windows, refreshed alongside the application search. */
let running = [];

/**
 * A stable two-letter monogram per item.
 *
 * Real icon extraction is not wired up. Deterministic on purpose: an icon that changed
 * between renders would make the list hard to scan.
 */
function monogram(name) {
    const words = String(name || '').trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '?';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
}

function render() {
    if (results.length === 0) {
        list.replaceChildren(
            Object.assign(document.createElement('div'), {
                className: 'empty',
                textContent: input.value ? 'Nothing matches that.' : 'No applications found.',
            }),
        );
        return;
    }

    const rows = results.map((item, i) => {
        const row = document.createElement('div');
        row.className = 'row' + (i === selected ? ' sel' : '');

        const glyph = document.createElement('div');
        glyph.className = 'glyph';
        glyph.textContent = monogram(item.name);

        const text = document.createElement('div');
        text.className = 'row-text';

        const name = document.createElement('div');
        name.className = 'row-name';
        name.textContent = item.name;

        const sub = document.createElement('div');
        sub.className = 'row-sub';
        sub.textContent = item.source || '';

        text.append(name, sub);

        const kind = document.createElement('div');
        kind.className = 'kind';
        kind.textContent = item.kind === 'window' ? 'window' : 'app';

        row.append(glyph, text, kind);

        // A pin control per row. Only offered for launchable items: a running window
        // has no target of its own, and offering a button that cannot work is worse
        // than not offering it.
        if (item.kind !== 'window' && item.target) {
            row.append(pinButton(item, i));
        }

        row.addEventListener('click', () => choose(i));
        // Mouse hover must move the selection, or clicking a row the keyboard has not
        // reached launches the wrong thing.
        row.addEventListener('mousemove', () => {
            if (selected !== i) {
                selected = i;
                render();
            }
        });

        return row;
    });

    list.replaceChildren(...rows);
    list.children[selected]?.scrollIntoView({ block: 'nearest' });
}

async function search(query) {
    const limit = query ? 30 : 40;
    let apps = [];
    try {
        apps = (await sdk.call('launcher.search', { query, limit })) || [];
    } catch (err) {
        console.error('launcher.search failed', err);
    }

    // Running windows come first when a query is present, because switching to something
    // already open is the common case and takes no launch at all.
    const windows = query
        ? running
            .filter((w) => (w.title || '').toLowerCase().includes(query.toLowerCase()))
            .slice(0, limit)
            .map((w) => ({ name: w.title || w.proc_name, target: null, kind: 'window', source: 'running', hwnd: w.hwnd }))
        : [];

    const merged = [...windows, ...apps].slice(0, limit);
    results = merged;
    selected = 0;
    render();
}

/** Whether an item is currently pinned, matched case-insensitively like the daemon. */
function isPinned(item) {
    return pinnedTargets.has(String(item.target || '').trim().toLowerCase());
}

/**
 * The per-row pin control.
 *
 * stopPropagation matters: the row's click handler launches, so without it a click
 * aimed at the pin would start the application instead.
 */
function pinButton(item, index) {
    const pinned = isPinned(item);
    const btn = document.createElement('button');
    btn.className = 'pin-btn' + (pinned ? ' on' : '');
    btn.textContent = pinned ? '★' : '☆';
    btn.title = pinned ? 'Unpin from dock' : 'Pin to dock';
    btn.addEventListener('click', (e) => {
        e.stopPropagation();
        togglePin(item, index);
    });
    return btn;
}

/** Pin or unpin an item, then redraw so every row's marker stays truthful. */
async function togglePin(item, index) {
    try {
        const res = await sdk.call('pins.toggle', { target: item.target, name: item.name });
        pinnedTargets = isPinned(item) ? withoutTarget(item) : withTarget(item);
        render();
        return res;
    } catch (err) {
        // The store can refuse, e.g. at its pin limit. Say so rather than showing a
        // star that implies the app was saved when it was not.
        console.error('pin failed', err);
        flashError(err.message || String(err));
    }
}

function withTarget(item) {
    const next = new Set(pinnedTargets);
    next.add(String(item.target || '').trim().toLowerCase());
    return next;
}

function withoutTarget(item) {
    const next = new Set(pinnedTargets);
    next.delete(String(item.target || '').trim().toLowerCase());
    return next;
}

/** Briefly show a message where the results are, then restore them. */
function flashError(message) {
    list.replaceChildren(
        Object.assign(document.createElement('div'), {
            className: 'empty',
            textContent: message,
        }),
    );
    setTimeout(() => {
        if (results.length) render();
    }, 2200);
}

/** Ask the daemon for the current pin list. */
async function loadPins() {
    try {
        const res = await sdk.call('pins.list');
        pinnedTargets = new Set(((res && res.pins) || []).map((p) => String(p.target).trim().toLowerCase()));
    } catch (err) {
        // A widget without pins.manage can still list. If even that fails, pinning is
        // simply unavailable here, which the absent pin buttons make obvious enough.
        console.warn('could not load pins', err);
        pinnedTargets = new Set();
    }
}

function scheduleSearch() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => search(input.value), 60);
}

async function choose(index) {
    const item = results[index];
    if (!item) return;

    try {
        if (item.kind === 'window') {
            // Focusing an existing window beats launching a second copy of it.
            await sdk.call('window.focus', { hwnd: item.hwnd });
        } else {
            await sdk.call('launcher.launch', { target: item.target });
        }
    } catch (err) {
        console.error('launch failed', err);
        return;
    }
    dismiss();
}

function dismiss() {
    // Tell the daemon to close us. Closing the window from the page directly would leave
    // the daemon holding a credential for a surface that no longer exists.
    if (boot.hasBridge) {
        window.chrome.webview.postMessage(DISMISS);
    }
}

function move(delta) {
    if (results.length === 0) return;
    selected = (selected + delta + results.length) % results.length;
    render();
}

function wireKeys() {
    input.addEventListener('keydown', (e) => {
        switch (e.key) {
            case 'ArrowDown': e.preventDefault(); move(1); break;
            case 'ArrowUp': e.preventDefault(); move(-1); break;
            case 'Enter': e.preventDefault(); choose(selected); break;
            case 'Escape': e.preventDefault(); dismiss(); break;
            // Pin or unpin the highlighted row without launching it. Ctrl rather than a
            // bare letter so it cannot shadow the query the user is still typing.
            case 'p':
            case 'P':
                if (e.ctrlKey) {
                    e.preventDefault();
                    const item = results[selected];
                    if (item && item.kind !== 'window' && item.target) togglePin(item, selected);
                }
                break;
        }
    });

    input.addEventListener('input', scheduleSearch);
}

async function main() {
    if (!boot || !boot.hasBridge) {
        list.replaceChildren(
            Object.assign(document.createElement('div'), {
                className: 'empty',
                textContent: 'No shell connection. The launcher needs the daemon.',
            }),
        );
        return;
    }

    try {
        await sdk.connect();
    } catch (err) {
        console.error('could not reach the daemon', err);
        return;
    }

    sdk.subscribe('windows.updated', (windows) => {
        running = windows || [];
        // Refresh the result set only while the query is active; otherwise there is
        // nothing on screen that would change.
        if (input.value) scheduleSearch();
    });

    // Keep the stars truthful when the dock is rearranged from the other surface.
    sdk.subscribe('pins.changed', (state) => {
        pinnedTargets = new Set(((state && state.pins) || []).map((p) => String(p.target).trim().toLowerCase()));
        render();
    });

    await loadPins();

    try {
        running = (await sdk.call('windows.get')) || [];
    } catch {
        running = [];
    }

    wireKeys();
    await search('');
    input.focus();
}

main();