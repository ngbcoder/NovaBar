# NovaBar Dynamic Island — progress

## Phase 1: Dynamic Island surface
- [x] Add `IslandConfig` and `SpotifyConfig` to `internal/config`.
- [x] Add config schema entries for island size/content and Spotify settings.
- [x] Create `widgets/island` widget (media + clock + date, hover expand).
- [x] Daemon: `showIsland`, `closeIsland`, `syncIsland`.
- [x] Wire island show/hide to media sessions in `mediaPump`.
- [x] Live-reload island on config changes.
- [x] Teardown island cleanly.
- [x] All tests pass (`build.ps1 verify`).

## Phase 1b: Embedded bar island
- [x] Bar center is a persistent Dynamic Island pill (clock/date always visible).
- [x] Media track info appears inside the pill when playing; pill widens and darkens.
- [x] Transport controls revealed on hover or click-to-expand.
- [x] Floating island widget deleted entirely (`widgets/island` removed, daemon overlay code removed).
- [x] Island config toggles (`show_media`, `show_clock`, `show_date`) now drive the bar pill.
- [x] `island.enabled`/`width`/`height` remain in config for schema compatibility but are inert.

## Phase 2: Spotify integration
- [x] Add `internal/spotify` package (OAuth2 PKCE, token storage, refresh).
- [x] Register fixed redirect URI callback (`http://127.0.0.1:8910/spotify/callback`).
- [x] Add RPC methods: status, queue, play, pause, next, previous, connect.
- [x] Permission gating for Spotify methods (reuses windows.read/media.control).
- [x] Island expanded view shows Spotify queue.
- [x] `spotify.prefer` toggle switches island source from GSMTC to Spotify.
- [x] Fallback to GSMTC when Spotify is unavailable or not connected.
- [ ] End-to-end test with a real Spotify client ID.

## Phase 3: Interruptibility / priority
- [ ] Define priority levels (media < notification < user action).
- [ ] Allow higher-priority items to replace island content temporarily.
- [ ] Restore previous item after dismissal.

## Notes
- GSMTC already reports `AppID` (source app) and `Source: "gsmtc"`.
- Spotify Web API scopes needed: `user-read-playback-state`, `user-read-currently-playing`, `user-modify-playback-state`.
- Redirect URI must be registered exactly in the Spotify developer dashboard.