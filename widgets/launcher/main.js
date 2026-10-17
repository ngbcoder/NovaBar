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