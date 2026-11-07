import { NovaBarSDK } from '/sdk/novabar.js';
const boot = window.NOVABAR;
const sdk = new NovaBarSDK({ port: boot.port, token: boot.token, widgetToken: boot.widget_token });

const title = document.getElementById('title');
const artist = document.getElementById('artist');
const overlay = document.getElementById('overlay');
const overlayTitle = document.getElementById('overlay-title');
const overlayBody = document.getElementById('overlay-body');

function renderMedia(session) {
  if (session && session.title) {
    title.textContent = session.title;
    artist.textContent = session.artist || '';
  } else {
    title.textContent = 'Nothing playing';
    artist.textContent = '';
  }
}

function renderIsland(payload) {
  const item = payload?.item ?? payload;
  if (item && item.kind !== 'media') {
    overlay.classList.remove('hidden');
    overlayTitle.textContent = item.title || '';
    overlayBody.textContent = item.body || '';
  } else {
    overlay.classList.add('hidden');
  }
}

async function refreshMedia() {
  try {
    const s = await sdk.call('media.get');
    renderMedia(s);
  } catch (e) { console.warn(e); }
}

async function refreshVolume() {
  try {
    const res = await sdk.call('volume.list');
    if (!res || !res.available) return;
    const master = res.master || {};
    const mv = document.getElementById('master-volume');
    const mm = document.getElementById('master-mute');
    if (mv) {
      mv.value = String(master.level ?? 1);
      mv.oninput = () => sdk.call('volume.master.set', { level: Number(mv.value) }).catch(()=>{});
    }
    if (mm) {
      mm.innerHTML = `<span class="material-icons-round">${master.muted ? 'volume_off' : 'volume_up'}</span>`;
      mm.onclick = () => sdk.call('volume.master.mute', { muted: !master.muted }).then(()=>refreshVolume()).catch(()=>{});
    }
    const sessionsEl = document.getElementById('sessions');
    if (sessionsEl) {
      sessionsEl.replaceChildren();
      for (const s of res.sessions || []) {
        if (s.system_sounds) continue;
        const row = document.createElement('div');
        row.className = 'session';
        const label = document.createElement('span');
        label.textContent = s.display_name || s.proc_name || `PID ${s.pid}`;
        const slider = document.createElement('input');
        slider.type = 'range'; slider.min = '0'; slider.max = '1'; slider.step = '0.01';
        slider.value = String(s.volume ?? 0);
        slider.oninput = () => sdk.call('volume.set', { pid: s.pid, level: Number(slider.value) }).catch(()=>{});
        row.append(label, slider);
        sessionsEl.appendChild(row);
      }
    }
  } catch (e) { console.warn(e); }
}

async function main() {
  if (!boot || !boot.hasBridge) return;
  try { await sdk.connect(); } catch (e) { console.error(e); return; }
  await refreshMedia();
  refreshVolume();
  sdk.subscribe('media.state', renderMedia);
  sdk.subscribe('island.state', renderIsland);
  try {
    const st = await sdk.call('island.get');
    renderIsland(st);
  } catch (e) {}

  document.getElementById('prev').addEventListener('click', () => sdk.call('media.command', { command: 'previous' }).catch(()=>{}));
  document.getElementById('toggle').addEventListener('click', () => sdk.call('media.command', { command: 'toggle' }).catch(()=>{}));
  document.getElementById('next').addEventListener('click', () => sdk.call('media.command', { command: 'next' }).catch(()=>{}));
  document.getElementById('close').addEventListener('click', () => {
    if (boot.hasBridge) window.chrome.webview.postMessage('__novabar_dismiss__');
  });
  document.getElementById('settings').addEventListener('click', () => {
    sdk.call('settings.open').catch(() => {});
  });

  // Battery via the Chromium Battery API.
  const batteryEl = document.getElementById('battery');
  if (navigator.getBattery) {
    navigator.getBattery().then((b) => {
      const update = () => { batteryEl.textContent = `Battery: ${Math.round(b.level * 100)}%${b.charging ? ' ⚡' : ''}`; };
      update();
      b.addEventListener('levelchange', update);
      b.addEventListener('chargingchange', update);
    }).catch(() => { batteryEl.textContent = 'Battery: unavailable'; });
  } else {
    batteryEl.textContent = 'Battery: unavailable';
  }
}
main();