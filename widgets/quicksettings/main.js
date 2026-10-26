import { NovaBarSDK } from '/sdk/novabar.js';

const DISMISS = '__novabar_dismiss__';
const SIZE_MESSAGE = 'quicksettings-size';
const QUEUE_REFRESH_MS = 5000;
// body has 10px padding above and below, and left and right, around the panel.
const BODY_PADDING = 20;

const boot = window.NOVABAR;
const sdk = new NovaBarSDK({
  port: boot?.port,
  token: boot?.token,
  widgetToken: boot?.widget_token,
});

const panel = document.querySelector('.panel');
const queueSection = document.getElementById('queue');
const queueList = document.getElementById('queue-list');
const queueTotal = document.getElementById('queue-total');

let lastReportedWidth = 0;
let lastReportedHeight = 0;
let queueInFlight = false;

function dismiss() {
  if (boot?.hasBridge && window.chrome?.webview) {
    window.chrome.webview.postMessage(DISMISS);
  }
}

async function openTarget(target) {
  try {
    await sdk.call('launcher.launch', { target });
  } catch (err) {
    console.error('launcher.launch failed', err);
  }
  // The settings app takes over from here, so the panel has done its job.
  dismiss();
}

document.addEventListener('click', (event) => {
  const button = event.target.closest('button[data-target]');
  if (!button) return;
  openTarget(button.dataset.target);
});

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') dismiss();
});

// The window starts at the panel's smallest size. Tell the daemon when the content
// needs more room, so the queue is not clipped beside the settings. The daemon owns the
// window rectangle because only it knows which bar the panel is anchored to.
function reportSize() {
  if (!panel) return;

  // offsetWidth/offsetHeight, not getBoundingClientRect: the panel's drop-in
  // animation starts scaled down, and a rect measured during it would report a
  // slightly smaller panel and leave a transparent border around the real one.
  const width = panel.offsetWidth + BODY_PADDING;
  const height = panel.offsetHeight + BODY_PADDING;
  if (Math.abs(width - lastReportedWidth) < 2 && Math.abs(height - lastReportedHeight) < 2) return;
  lastReportedWidth = width;
  lastReportedHeight = height;

  if (boot?.hasBridge && window.chrome?.webview) {
    window.chrome.webview.postMessage(JSON.stringify({
      type: SIZE_MESSAGE,
      width,
      height,
    }));
  }
}

function watchSize() {
  reportSize();
  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(reportSize).observe(panel);
  }
}

// showQueueNote keeps the column and its size when there is nothing to list.
// Hiding it here is what used to make the panel shrink a moment after it opened.
function showQueueNote(message) {
  if (!queueSection || !queueList || !queueTotal) return;

  queueSection.hidden = false;
  queueSection.removeAttribute('aria-busy');
  queueTotal.textContent = '';

  const note = document.createElement('li');
  note.className = 'queue-note';
  note.textContent = message;
  queueList.replaceChildren(note);
}

function renderQueue(tracks) {
  if (!queueSection || !queueList || !queueTotal) return;

  const items = Array.isArray(tracks) ? tracks : [];
  queueList.replaceChildren();

  if (items.length === 0) {
    showQueueNote('Nothing up next');
    return;
  }

  queueSection.hidden = false;
  queueSection.removeAttribute('aria-busy');
  queueTotal.textContent = String(items.length);

  items.forEach((track, index) => {
    const item = document.createElement('li');
    item.className = 'queue-item';

    const number = document.createElement('span');
    number.className = 'queue-index';
    number.textContent = String(index + 1);

    const text = document.createElement('span');
    text.className = 'queue-text';

    const title = document.createElement('span');
    title.className = 'queue-title';
    title.textContent = track?.name || 'Unknown track';

    const artists = Array.isArray(track?.artists) ? track.artists.filter(Boolean) : [];
    const artist = document.createElement('span');
    artist.className = 'queue-artist';
    artist.textContent = artists.length > 0 ? artists.join(', ') : (track?.album || '');

    text.append(title, artist);
    item.append(number, text);
    queueList.append(item);
  });
}

async function refreshQueue() {
  if (!sdk.connected || queueInFlight) return;

  queueInFlight = true;
  try {
    const result = await sdk.call('spotify.queue');
    renderQueue(result?.tracks);
  } catch (err) {
    // Spotify may be unconfigured, logged out, or have no active playback. None of
    // those should leave a stale queue on screen, but the column stays: the panel was
    // already sized for it and collapsing it now would resize the window under the
    // pointer.
    console.warn('[NovaBar] spotify.queue failed', err);
    showQueueNote('Queue unavailable');
  } finally {
    queueInFlight = false;
  }
}

async function main() {
  watchSize();

  if (!boot || !boot.hasBridge) {
    console.info('[NovaBar] No daemon bridge; quick settings rendered statically.');
    showQueueNote('Queue unavailable');
    return;
  }

  try {
    await sdk.connect();
  } catch (err) {
    console.error('quick settings connect failed', err);
    showQueueNote('Queue unavailable');
    return;
  }

  // Move focus into the panel so screen readers announce it and keyboard users
  // land somewhere sensible (the panel itself, then Tab through the tiles).
  try { panel.focus(); } catch (_) { /* ignore */ }

  // The queue follows the same pause as the rest of the panel: it is only fetched
  // while this page is alive, which is exactly while the panel is open.
  refreshQueue();
  window.setInterval(refreshQueue, QUEUE_REFRESH_MS);
  // A skip changes what is up next, so refresh as soon as the track changes rather
  // than waiting for the next poll.
  sdk.subscribe('media.state', () => refreshQueue());
}

main();
