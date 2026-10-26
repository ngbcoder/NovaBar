# Widgets

A widget is a directory under `widgets/` with an `index.html` and an optional
`manifest.json`. There is no build step and no bundler: the daemon serves the
directory over loopback and a surface navigates to it.

## Manifest

```json
{ "permissions": ["windows.read", "windows.focus", "audio.control"] }
```

Available permissions: `windows.read`, `windows.control`, `workspaces.manage`,
`launcher.use`, `media.control`, `config.read`.

An unknown permission in a manifest is rejected and the widget is denied everything.
A method with no permission mapping is not callable by widgets at all.

## RPC

The RPC endpoint and session token are **injected** into the page by the daemon before
it loads, as `window.NOVABAR`. They are not written into any file.

```js
const sdk = new NovaBarSDK(await fetchConfig());
sdk.connect();
await sdk.call("windows.get");
sdk.subscribe("windows.updated", onWindows);
```

` sdk/novabar.js` is served from `/sdk` and imported directly. Widget assets are served
without the session token.

## Built-in widgets

- `bar` — top/bottom bar with Dynamic Island pill, media, clock, date.
- `dock` — overlay dock.
- `launcher` — application search and launch.
- `settings` — in-shell settings overlay.
- `zones` — workspace zones.