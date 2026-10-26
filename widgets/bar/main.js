// The NovaBar default bar widget.
//
// Everything here talks to the daemon over the loopback JSON-RPC interface described in
// sdk/novabar.ts. The endpoint and session token are injected by the daemon as
// window.NOVABAR before this module runs; there are no secrets in this file.

import { NovaBarSDK } from '/sdk/novabar.js';

const boot = window.NOVABAR;

const el = {
  workspaces: document.getElementById('workspaces'),
  island: document.getElementById('island'),
  islandClock: document.getElementById('island-clock'),
  islandDate: document.getElementById('island-date'),
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

/** Content toggles, driven by the island section of the config. */
let showMedia = true;
let showClock = true;
let showDate = true;

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
      // Numbered circular pills matching the target look
      btn.textContent = String(w.id);
      btn.title = `${w.name} – ${w.window_count} window${w.window_count === 1 ? '' : 's'}`;
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

function renderMedia(session) {
  if (!showMedia) {
    el.media.hidden = true;
    el.island.classList.remove('has-media');
    return;
  }
  const hasMedia = Boolean(session && session.title && session.source && session.source !== 'none');
  el.media.hidden = !hasMedia;
  el.island.classList.toggle('has-media', hasMedia);
  if (!hasMedia) return;

  el.mediaTitle.textContent = session.title;
  el.mediaArtist.textContent = session.artist || '';
  el.mediaToggle.classList.toggle('is-playing', session.playback === 'playing');
  el.mediaNext.disabled = session.can_next === false;
  el.mediaPrev.disabled = session.can_previous === false;
}

function mediaCommand(command) {
  return sdk.call('media.command', { command }).catch((err) => {
    console.error('media.command failed', err);
  });
}

async function refreshMedia() {
  try {
    const session = await sdk.call('media.get');
    renderMedia(session);
  } catch (err) {
    console.warn('media.get failed', err);
  }
}

async function loadConfig() {
  try {
    const cfg = await sdk.call('config.get');
    const island = cfg?.island || {};
    showMedia = island.show_media !== false;
    showClock = island.show_clock !== false;
    showDate = island.show_date !== false;
  } catch (err) {
    console.warn('config.get failed', err);
  }
  renderClock();
}

function renderClock() {
  const now = new Date();
  el.islandClock.textContent = showClock
    ? now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : '';
  el.islandClock.style.display = showClock ? '' : 'none';
  el.islandDate.textContent = showDate
    ? now.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
    : '';
  el.islandDate.style.display = showDate ? '' : 'none';
}

function wireControls() {
  el.mediaToggle.addEventListener('click', (e) => {
    e.stopPropagation();
    mediaCommand('toggle');
  });
  el.mediaNext.addEventListener('click', (e) => {
    e.stopPropagation();
    mediaCommand('next');
  });
  el.mediaPrev.addEventListener('click', (e) => {
    e.stopPropagation();
    mediaCommand('previous');
  });

  // Click the island pill to toggle expanded state (reveals transport controls).
  el.island.addEventListener('click', () => {
    el.island.classList.toggle('expanded');
  });
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

  await loadConfig();

  sdk.subscribe('workspace.changed', (state) => renderWorkspaces(state.workspaces || []));
  sdk.subscribe('media.state', renderMedia);

  await Promise.all([refreshWorkspaces(), refreshMedia()]);
}

main();
