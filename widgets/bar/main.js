// The NovaBar default bar widget with integrated weather pill.
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
  weather: document.getElementById('weather'),
  weatherTemp: document.getElementById('weather-temp'),
  media: document.getElementById('media'),
  mediaSlot: document.querySelector('.media-slot'),
  mediaInfo: document.getElementById('media-info'),
  mediaArt: document.getElementById('media-art'),
  mediaArtImg: document.getElementById('media-art-img'),
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
let showWeather = true;

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

/** The last session pushed by the daemon, so a config change can re-render it. */
let lastSession = null;

/** Album art currently in the <img>, and the URLs that already failed to load. */
let currentArtwork = null;
const failedArtwork = new Set();

// How long the now-playing block takes to collapse. The width transition runs for
// 360ms, so the text and cover have to survive a little longer than that.
const MEDIA_COLLAPSE_MS = 420;

/** Pending clear of the last track, cancelled when playback comes back. */
let mediaClearTimer = 0;

function clearMediaContent() {
  el.mediaTitle.textContent = '';
  el.mediaArtist.textContent = '';
  el.mediaInfo.removeAttribute('title');
  setArtwork('');
}

function setArtwork(url) {
  // Spotify hands out a URL per album. A broken one must not be retried on every
  // media broadcast, which arrives continuously while a track plays.
  const next = url && !failedArtwork.has(url) ? url : '';
  if (next === currentArtwork) return;
  currentArtwork = next;

  if (!next) {
    el.mediaArt.classList.add('is-empty');
    el.mediaArtImg.removeAttribute('src');
    return;
  }
  // Deliberately not clearing the empty state here: the previous cover stays on
  // screen until the new one has decoded, instead of flashing the placeholder.
  el.mediaArtImg.src = next;
}

function renderMedia(session) {
  lastSession = session || null;

  const hasMedia = Boolean(
    showMedia && session && session.title && session.source && session.source !== 'none',
  );
  el.island.classList.toggle('has-media', hasMedia);

  if (!hasMedia) {
    el.island.classList.remove('is-playing');

    // Keep the title, artist and cover in the DOM until the block has finished
    // collapsing. Emptying them here drops the grid track's content width to zero
    // immediately, so there is nothing left for the width transition to animate and
    // the island snaps back to the clock.
    el.mediaSlot.setAttribute('aria-hidden', 'true');
    window.clearTimeout(mediaClearTimer);
    mediaClearTimer = window.setTimeout(clearMediaContent, MEDIA_COLLAPSE_MS);
    return;
  }

  window.clearTimeout(mediaClearTimer);
  el.mediaSlot.removeAttribute('aria-hidden');

  el.mediaTitle.textContent = session.title;
  el.mediaArtist.textContent = session.artist || session.album || '';
  el.mediaInfo.title = [session.title, session.artist, session.album]
    .filter(Boolean)
    .join(' — ');

  const playing = session.playback === 'playing';
  el.mediaToggle.classList.toggle('is-playing', playing);
  el.island.classList.toggle('is-playing', playing);
  el.mediaNext.disabled = session.can_next === false;
  el.mediaPrev.disabled = session.can_previous === false;
  setArtwork(session.artwork);
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

async function refreshWeather() {
  if (!showWeather) {
    el.weather.hidden = true;
    return;
  }
  try {
    // Coordinates can be customized or pulled from daemon configuration
    const lat = 40.7128;
    const lon = -74.0060;
    const res = await fetch(`https://api.open-meteo.com/v1/forecast?latitude=${lat}&longitude=${lon}&current=temperature_2m&temperature_unit=fahrenheit`);
    const data = await res.json();
    const temp = Math.round(data.current.temperature_2m);

    el.weatherTemp.textContent = `${temp}°F`;
    el.weather.hidden = false;
  } catch (err) {
    console.warn('weather fetch failed', err);
    el.weather.hidden = true;
  }
}

async function loadConfig() {
  try {
    const cfg = await sdk.call('config.get');
    const island = cfg?.island || {};
    showMedia = island.show_media !== false;
    showClock = island.show_clock !== false;
    showDate = island.show_date !== false;
    showWeather = island.show_weather !== false;
  } catch (err) {
    console.warn('config.get failed', err);
  }
  renderClock();
  renderMedia(lastSession);
  refreshWeather();
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
  el.mediaArtImg.addEventListener('load', () => {
    el.mediaArt.classList.remove('is-empty');
  });
  el.mediaArtImg.addEventListener('error', () => {
    if (currentArtwork) failedArtwork.add(currentArtwork);
    el.mediaArt.classList.add('is-empty');
  });

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

  // The island is drawn inside the bar window, which is only 40px tall. Expanding it
  // in place would be clipped by the Win32 surface, so the daemon opens the quick
  // settings panel as a separate overlay just below the bar.
  el.island.addEventListener('click', toggleQuickSettings);
  el.island.addEventListener('keydown', (event) => {
    if (event.target !== el.island) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleQuickSettings();
    }
  });
}

function toggleQuickSettings() {
  if (!boot || !boot.hasBridge || !window.chrome?.webview) return;
  window.chrome.webview.postMessage(JSON.stringify({ type: 'toggle-quick-settings' }));
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

  // Fetch weather immediately and refresh every 15 minutes
  refreshWeather();
  setInterval(refreshWeather, 15 * 60 * 1000);

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