// The NovaBar floating dock.
//
// A macOS-style dock: a translucent rounded pill floating over the wallpaper, with
// magnification on hover, running dots, and a separator between pinned applications and
// running windows. The window reserves no work area, so windows underneath stay fully
// usable, and it is sized to the dock rather than to the screen, so the rest of the
// bottom strip stays clickable.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;
const dock = document.getElementById('dock');
const hotzone = document.getElementById('hotzone');

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Auto-hide: dock slides away after the pointer leaves; hotzone at the bottom reveals it. */
const HIDE_DELAY_MS = 700;
let hideTimer = 0;
let dockVisible = true;

/**
 * Report the width of the panel so the shell can size the window to it.
 *
 * The window has to be as wide as the dock and no wider. A window as wide as the
 * screen covers the whole strip along the bottom of the display, and a window that
 * covers the screen takes the clicks: nothing underneath it could be pressed, which
 * is the difference between a dock and a sheet of glass laid over the desktop.
 *
 * The natural width is what is reported, never the magnified one. The magnification
 * grows the icons outwards, and a window that grew and shrank with the pointer would
 * be a window that flickered.
 */
let lastReportedWidth = 0;

function reportWidth() {
    if (dock.classList.contains('magnifying')) return;

    const width = Math.ceil(Math.max(dock.scrollWidth, dock.offsetWidth));
    if (width <= 0 || width === lastReportedWidth) return;

    lastReportedWidth = width;
    window.chrome.webview.postMessage(JSON.stringify({ type: 'dock-width', width }));
}

/** Watch the panel for size changes, so the window follows the number of icons. */
function watchWidth() {
    if (typeof ResizeObserver !== 'function') return;
    new ResizeObserver(reportWidth).observe(dock);
}

function showDock() {
    if (hideTimer) {
        clearTimeout(hideTimer);
        hideTimer = 0;
    }
    dockVisible = true;
    dock.classList.remove('hidden');
}

function scheduleHide() {
    if (hideTimer) clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
        hideTimer = 0;
        dockVisible = false;
        dock.classList.add('hidden');
        dismissMenu();
    }, HIDE_DELAY_MS);
}

function wireAutoHide() {
    // Reveal when the pointer enters the bottom strip or the dock itself.
    const reveal = () => showDock();
    const maybeHide = () => scheduleHide();

    hotzone.addEventListener('pointerenter', reveal);
    dock.addEventListener('pointerenter', reveal);
    dock.addEventListener('pointerleave', maybeHide);

    // If the pointer leaves the whole overlay (window) while dock is up, hide.
    document.documentElement.addEventListener('pointerleave', maybeHide);

    // Start visible briefly so the user sees it on launch, then auto-hide.
    setTimeout(() => {
        if (!dock.matches(':hover') && !hotzone.matches(':hover')) {
            scheduleHide();
        }
    }, 1800);
}

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
 * Real icon extraction is wired up via Go backend, falling back to a stable two-letter monogram if needed.
 */
function monogram(name) {
    const words = String(name || '').trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '?';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
}

/**
 * A stable hue (0-359) for a name, so each monogram tile keeps its own colour
 * between renders and between sessions instead of every tile being the same grey.
 * Shifted toward the cyan/teal family to match the target palette.
 */
function hueFor(name) {
    let h = 0;
    for (const ch of String(name || '')) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
    // Bias into cooler tones (cyan / teal / blue-green) while keeping variety
    return 140 + ((h * 137) % 100);
}

/** Hide any open custom context menu. */
function dismissMenu() {
    document.querySelectorAll('.ctx-menu').forEach((el) => el.remove());
}

/**
 * Show a small glass context menu next to a tile.
 * items: [{ label, action, danger? }]
 */
function showContextMenu(e, items) {
    dismissMenu();
    if (!items || items.length === 0) return;

    const menu = document.createElement('div');
    menu.className = 'ctx-menu';
    for (const item of items) {
        const row = document.createElement('button');
        row.className = 'ctx-item' + (item.danger ? ' danger' : '');
        row.textContent = item.label;
        row.addEventListener('click', (ev) => {
            ev.stopPropagation();
            dismissMenu();
            item.action();
        });
        menu.appendChild(row);
    }

    document.body.appendChild(menu);

    // Position above the pointer so it stays inside the dock overlay when possible.
    const pad = 8;
    const mw = menu.offsetWidth;
    const mh = menu.offsetHeight;
    let x = e.clientX - mw / 2;
    let y = e.clientY - mh - 12;
    x = Math.max(pad, Math.min(x, window.innerWidth - mw - pad));
    y = Math.max(pad, y);
    menu.style.left = `${x}px`;
    menu.style.top = `${y}px`;

    // Dismiss on next outside click / escape / leave dock.
    const close = (ev) => {
        if (ev.type === 'keydown' && ev.key !== 'Escape') return;
        if (ev.type === 'pointerdown' && menu.contains(ev.target)) return;
        dismissMenu();
        document.removeEventListener('pointerdown', close, true);
        document.removeEventListener('keydown', close, true);
    };
    // Defer so the current contextmenu event does not immediately close it.
    requestAnimationFrame(() => {
        document.addEventListener('pointerdown', close, true);
        document.addEventListener('keydown', close, true);
    });
}

function makeTile({ label, glyph, icon, hue, running: isRunning, active, onClick, menuItems, onWheel, volume }) {
    const btn = document.createElement('button');
    btn.className = 'tile' + (active ? ' active' : '') + (icon ? ' has-icon' : '');
    btn.style.setProperty('--h', String(hue ?? 168));

    if (icon) {
        const img = document.createElement('img');
        img.className = 'glyph-img';
        img.src = icon;
        btn.appendChild(img);
    } else {
        const g = document.createElement('div');
        g.className = 'glyph';
        g.textContent = glyph;
        btn.appendChild(g);
    }

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

    // Always kill the WebView/browser context menu so the shell does not look broken.
    btn.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (menuItems && menuItems.length) showContextMenu(e, menuItems);
    });

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
            icon: app.icon, // Passes the extracted base64 icon data URI
            hue: hueFor(app.proc_name || app.name),
            running: running.some((w) => w.proc_name === app.proc_name),
            active: running.some((w) => w.proc_name === app.proc_name && w.hwnd === activeHwnd),
            onClick: () => launch(app.target),
            menuItems: [
                { label: 'Unpin from dock', action: () => unpin(app), danger: true },
            ],
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
            hue: hueFor(win.proc_name || win.title),
            running: true,
            active: win.hwnd === activeHwnd,
            onClick: () => focus(win.hwnd),
            // Running tiles: suppress browser menu; pin needs an exe path we do not have yet.
            menuItems: [
                { label: 'Focus', action: () => focus(win.hwnd) },
            ],
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

    const reset = () => {
        dock.classList.remove('magnifying');
        tiles.forEach((t) => {
            t.style.transform = '';
            t.style.marginLeft = '';
            t.style.marginRight = '';
            t.style.removeProperty('--s');
        });
    };

    let frame = 0;
    let pointerX = 0;

    const apply = () => {
        frame = 0;

        // Read everything first, then write, so the browser lays out once per frame
        // instead of once per tile. offsetWidth is the untransformed layout; a scaled
        // tile's bounding box would make the falloff drift as it runs.
        const base = tiles.map((t) => t.offsetWidth);
        const centres = tiles.map((t) => {
            const r = t.getBoundingClientRect();
            return r.left + r.width / 2;
        });

        let grew = false;
        const scales = tiles.map((_, i) => {
            const distance = Math.abs(pointerX - centres[i]) / (base[i] + 4);
            if (distance > MAGNIFY_SPAN) return 1;
            const scale = 1 + (MAGNIFY_MAX - 1) * Math.cos((distance / MAGNIFY_SPAN) * (Math.PI / 2));
            if (scale > 1.001) grew = true;
            return scale;
        });

        if (!grew) { reset(); return; }

        dock.classList.add('magnifying');
        tiles.forEach((tile, i) => {
            const scale = scales[i];
            tile.style.transform = `scale(${scale})`;
            // --s lets the tooltip counter-scale so its text is never blown up with the tile.
            tile.style.setProperty('--s', scale.toFixed(3));
            // Half the growth goes to each side, taken from the tile's own margin, so the
            // dock widens under the pointer rather than icons overlapping.
            const extra = (base[i] * (scale - 1)) / 2;
            tile.style.marginLeft = `${extra}px`;
            tile.style.marginRight = `${extra}px`;
        });
    };

    dock.onpointermove = (e) => {
        pointerX = e.clientX;
        if (!frame) frame = requestAnimationFrame(apply);
    };

    dock.onpointerleave = () => {
        if (frame) { cancelAnimationFrame(frame); frame = 0; }
        reset();
    };
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
    // dismiss and Escape must not close it. Also kill the WebView browser menu
    // everywhere on this surface so right-click never looks broken.
    document.addEventListener('keydown', (e) => e.preventDefault());
    document.addEventListener('contextmenu', (e) => e.preventDefault());

    wireAutoHide();

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

    // The window is sized to the dock, so the shell has to be told how wide it is.
    watchWidth();
    reportWidth();
}

main();