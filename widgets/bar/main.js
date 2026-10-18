// The NovaBar default bar widget.
//
// Everything here talks to the daemon over the loopback JSON-RPC interface described in
// sdk/novabar.ts. The endpoint and session token are injected by the daemon as
// window.NOVABAR before this module runs; there are no secrets in this file.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;

const el = {
  workspaces: document.getElementById('workspaces'),
  title: document.getElementById('window-title'),
  clock: document.getElementById('clock'),
  offline: document.getElementById('offline'),
  media: document.getElementById('media'),
  mediaTitle: document.getElementById('media-title'),
  mediaArtist: document.getElementById('media-artist'),
  mediaToggle: document.getElementById('media-toggle'),
  mediaNext: document.getElementById('media-next'),
  mediaPrev: document.getElementById('media-prev'),
};

const sdk = new NovaBarSDK({
    port: boot.port,
    token: boot.token,
    widgetToken: boot.widget_token,
});

/** Workspaces currently known to the daemon, so buttons can be rebuilt without flicker. */
let knownWorkspaces = [];

/** Handle of the focused window, used to label the centre of the bar. */
let focusedHwnd = 0;

function setOffline(offline) {
  el.offline.classList.toggle('show', offline);
}

function renderWorkspaces(workspaces) {
  // Rebuild only when the set actually changed. Redrawing on every windows.updated
  // would make the buttons flicker under a mouse cursor.
  const ids = workspaces.map((w) => w.id).join(',');
  if (el.workspaces.dataset.signature === ids) {
    for (const w of workspaces) {
      el.workspaces.querySelector(`[data-ws="${w.id}"]`)?.classList.toggle('active', w.active);
    }
    return;
  }
  el.workspaces.dataset.signature = ids;

  el.workspaces.replaceChildren(
    ...workspaces.map((w) => {
      const btn = document.createElement('button');
      btn.className = 'ws' + (w.active ? ' active' : '');
      btn.dataset.ws = String(w.id);
      btn.textContent = w.name;
      btn.title = `${w.name} - ${w.window_count} window${w.window_count === 1 ? '' : 's'}`;
      btn.addEventListener('click', () => switchWorkspace(w.id));
      return btn;
    }),
  );
}

async function switchWorkspace(id) {
  try {
    await sdk.call('workspace.switch', { id });
  } catch (err) {
    console.error('workspace.switch failed', err);
  }
}

function renderWindows(windows) {
  const focused = windows.find((w) => w.hwnd === focusedHwnd);
  if (focused?.title) {
    el.title.textContent = focused.title;
    return;
  }
  // No specific window focused: show the newest one so the bar is not just blank.
  const recent = windows[windows.length - 1];
  el.title.textContent = recent?.title || 'NovaBar';
}

function renderMedia(session) {
  const hasMedia = session && session.source === 'gsmtc' && session.title;
  el.media.hidden = !hasMedia;
  if (!hasMedia) return;

  el.mediaTitle.textContent = session.title;
  el.mediaArtist.textContent = session.artist || '';
  el.mediaToggle.textContent = session.playback === 'playing' ? '\u23F8\uFE0E' : '\u25B6\uFE0E';
  el.mediaNext.disabled = !session.can_next;
  el.mediaPrev.disabled = !session.can_previous;
}

function renderClock() {
  el.clock.textContent = new Date().toLocaleTimeString();
}

function mediaCommand(command) {
  return sdk.call('media.command', { command }).catch((err) => {
    console.error('media.command failed', err);
  });
}

function wireControls() {
  el.mediaToggle.addEventListener('click', () => mediaCommand('toggle'));
  el.mediaNext.addEventListener('click', () => mediaCommand('next'));
  el.mediaPrev.addEventListener('click', () => mediaCommand('previous'));
  el.title.style.pointerEvents = 'auto';
}

async function refreshWorkspaces() {
  try {
    const state = await sdk.call('workspace.get');
    knownWorkspaces = state.workspaces || [];
    renderWorkspaces(knownWorkspaces);
  } catch (err) {
    console.error('workspace.get failed', err);
  }
}

async function refreshWindows() {
  try {
    const windows = await sdk.call('windows.get');
    renderWindows(windows || []);
  } catch (err) {
    console.error('windows.get failed', err);
  }
}

async function refreshMedia() {
  try {
    const session = await sdk.call('media.get');
    renderMedia(session);
  } catch (err) {
    // Media is optional; a failure here must not break the bar.
    console.warn('media.get failed', err);
  }
}

async function main() {
  // The clock is local and independent of the daemon, so it runs even if the
  // connection never comes up.
  renderClock();
  setInterval(renderClock, 1000);

  // In a plain browser there is no daemon and no bridge. Render the clock and stop,
  // rather than hammering a port nothing is listening on.
  if (!boot || !boot.hasBridge) {
    console.info('[NovaBar] No daemon bridge; rendering the bar statically.');
    return;
  }

  wireControls();

  try {
    await sdk.connect();
    setOffline(false);
  } catch (err) {
    setOffline(true);
    console.error('could not reach the daemon', err);
    return;
  }

  sdk.subscribe('windows.updated', renderWindows);
  sdk.subscribe('workspace.changed', (state) => renderWorkspaces(state.workspaces || []));
  sdk.subscribe('media.state', renderMedia);

  await Promise.all([refreshWorkspaces(), refreshWindows(), refreshMedia()]);
}

main();