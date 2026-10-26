package daemon

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/media"
	"github.com/ngbcoder/NovaBar/internal/spotify"
)

// TestSpotifySessionMapsTrack pins the Spotify -> MediaSession conversion the island
// renders. Playback is derived from IsPlaying rather than assumed, so a paused track
// still shows its title without claiming to be playing.
func TestSpotifySessionMapsTrack(t *testing.T) {
	playing := spotifySession(spotify.Track{
		Name:      "Training Season",
		Artists:   []string{"Dua Lipa"},
		Album:     "Radical Optimism",
		Artwork:   "https://i.scdn.co/image/cover",
		IsPlaying: true,
	})
	if playing.Source != "spotify" || playing.Title != "Training Season" {
		t.Fatalf("unexpected session: %+v", playing)
	}
	if playing.Artist != "Dua Lipa" {
		t.Errorf("artist = %q, want %q", playing.Artist, "Dua Lipa")
	}
	if playing.Artwork != "https://i.scdn.co/image/cover" {
		t.Errorf("artwork = %q, want the track's cover", playing.Artwork)
	}
	if playing.Playback != media.StatePlaying {
		t.Errorf("playback = %q, want %q", playing.Playback, media.StatePlaying)
	}

	paused := spotifySession(spotify.Track{Name: "Track", IsPlaying: false})
	if paused.Playback != media.StatePaused {
		t.Errorf("paused playback = %q, want %q", paused.Playback, media.StatePaused)
	}
}

// TestCurrentMediaSessionFallsBackToGSMTC covers the plain case: no Spotify client,
// so the controller's session is what the island sees.
func TestCurrentMediaSessionFallsBackToGSMTC(t *testing.T) {
	controller := &media.Controller{}
	controller.SetSession(media.MediaSession{
		Title:    "Local track",
		Playback: media.StatePlaying,
		Source:   "gsmtc",
	})

	d := &daemon{media: controller}
	got := d.currentMediaSession()
	if got.Source != "gsmtc" || got.Title != "Local track" {
		t.Fatalf("currentMediaSession() = %+v, want the GSMTC session", got)
	}
}

// TestMediaSessionUsable guards the rule that keeps the island hidden when there is
// nothing real to show.
func TestMediaSessionUsable(t *testing.T) {
	cases := []struct {
		name    string
		session media.MediaSession
		want    bool
	}{
		{"empty", media.MediaSession{}, false},
		{"none source", media.MediaSession{Source: "none", Title: "x"}, false},
		{"no title", media.MediaSession{Source: "gsmtc"}, false},
		{"gsmtc", media.MediaSession{Source: "gsmtc", Title: "x"}, true},
		{"spotify", media.MediaSession{Source: "spotify", Title: "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaSessionUsable(tc.session); got != tc.want {
				t.Errorf("mediaSessionUsable(%+v) = %v, want %v", tc.session, got, tc.want)
			}
		})
	}
}

// TestResolveSpotifyCommand pins toggle semantics. Mapping toggle straight to Play
// made the island's pause button a no-op and reported success while music kept going.
func TestResolveSpotifyCommand(t *testing.T) {
	playing := media.MediaSession{Source: "spotify", Playback: media.StatePlaying}
	paused := media.MediaSession{Source: "spotify", Playback: media.StatePaused}

	cases := []struct {
		name    string
		cmd     media.TransportCommand
		session media.MediaSession
		want    media.TransportCommand
	}{
		{"toggle while playing pauses", media.CommandTogglePlay, playing, media.CommandPause},
		{"toggle while paused plays", media.CommandTogglePlay, paused, media.CommandPlay},
		{"stop pauses", media.CommandStop, playing, media.CommandPause},
		{"play passes through", media.CommandPlay, playing, media.CommandPlay},
		{"next passes through", media.CommandNext, playing, media.CommandNext},
		{"previous passes through", media.CommandPrevious, playing, media.CommandPrevious},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSpotifyCommand(tc.cmd, tc.session); got != tc.want {
				t.Errorf("resolveSpotifyCommand(%q, %+v) = %q, want %q", tc.cmd, tc.session, got, tc.want)
			}
		})
	}
}

// TestMediaSessionChanged is the broadcast trigger. Position moves continuously and
// must not cause a notification, but every visible field must.
func TestMediaSessionChanged(t *testing.T) {
	base := media.MediaSession{
		Title:       "Track",
		Artist:      "Artist",
		Album:       "Album",
		Playback:    media.StatePlaying,
		Position:    10,
		CanNext:     true,
		CanPrevious: true,
		Source:      "spotify",
	}

	positionOnly := base
	positionOnly.Position = 42
	if mediaSessionChanged(base, positionOnly) {
		t.Error("a position change must not trigger a broadcast")
	}

	if mediaSessionChanged(base, base) {
		t.Error("identical sessions must not trigger a broadcast")
	}

	changes := map[string]media.MediaSession{
		"title":     func() media.MediaSession { s := base; s.Title = "Other"; return s }(),
		"artist":    func() media.MediaSession { s := base; s.Artist = "Other"; return s }(),
		"album":     func() media.MediaSession { s := base; s.Album = "Other"; return s }(),
		"artwork":   func() media.MediaSession { s := base; s.Artwork = "https://example/cover.jpg"; return s }(),
		"playback":  func() media.MediaSession { s := base; s.Playback = media.StatePaused; return s }(),
		"source":    func() media.MediaSession { s := base; s.Source = "none"; return s }(),
		"can next":  func() media.MediaSession { s := base; s.CanNext = false; return s }(),
		"can prev":  func() media.MediaSession { s := base; s.CanPrevious = false; return s }(),
		"can play":  func() media.MediaSession { s := base; s.CanPlay = true; return s }(),
		"can pause": func() media.MediaSession { s := base; s.CanPause = true; return s }(),
		"app id":    func() media.MediaSession { s := base; s.AppID = "Other.exe"; return s }(),
	}
	for name, session := range changes {
		if !mediaSessionChanged(base, session) {
			t.Errorf("%s change must trigger a broadcast", name)
		}
	}
}

// TestWithSpotifyArtworkKeepsExistingArt makes sure a session that already has a cover
// is never sent looking for another one. The Spotify branch of the resolver produces
// sessions with art, and re-fetching it would spend a request per poll.
func TestWithSpotifyArtworkKeepsExistingArt(t *testing.T) {
	d := &daemon{}
	s := media.MediaSession{
		AppID:   "Spotify.exe",
		Source:  "spotify",
		Title:   "Track",
		Artwork: "https://i.scdn.co/image/cover",
	}

	got := d.withSpotifyArtwork(s)
	if got.Artwork != s.Artwork {
		t.Errorf("artwork = %q, want the original %q", got.Artwork, s.Artwork)
	}
}

// TestWithSpotifyArtworkLeavesPlayersWithoutSpotifyAlone keeps the Web API out of the
// path when there is no Spotify login to ask: nothing can be borrowed.
func TestWithSpotifyArtworkLeavesPlayersWithoutSpotifyAlone(t *testing.T) {
	d := &daemon{}
	s := media.MediaSession{AppID: "foobar2000.exe", Source: "gsmtc", Title: "Track"}

	if got := d.withSpotifyArtwork(s); got.Artwork != "" {
		t.Errorf("artwork = %q, want none when Spotify is not connected", got.Artwork)
	}
}

// TestSameTrack covers the identity check that decides whether the API's cover may be
// drawn on a local session.
//
// The browser case is the one that matters: the Spotify web player is reported by
// GSMTC as the hosting browser, and it truncates a collaboration to its first artist.
// Rejecting those would leave the island without art, which is what it already did;
// accepting anything would put Spotify's cover on unrelated music.
func TestSameTrack(t *testing.T) {
	api := spotify.Track{
		Name:    "Essence of the Forest",
		Artists: []string{"Mondo Loops", "Purrple Cat"},
		Album:   "Forest Tales",
	}

	cases := []struct {
		name    string
		session media.MediaSession
		match   bool
	}{
		{
			name:    "browser playing the web player, first artist only",
			session: media.MediaSession{AppID: "Chrome", Title: "Essence of the Forest", Artist: "Mondo Loops"},
			match:   true,
		},
		{
			name:    "desktop client with every artist",
			session: media.MediaSession{AppID: "Spotify.exe", Title: "essence of the forest", Artist: "Mondo Loops, Purrple Cat"},
			match:   true,
		},
		{
			name:    "no artist reported",
			session: media.MediaSession{AppID: "Chrome", Title: "Essence of the Forest"},
			match:   true,
		},
		{
			name:    "another track",
			session: media.MediaSession{AppID: "Chrome", Title: "Something Else", Artist: "Mondo Loops"},
			match:   false,
		},
		{
			name:    "same title, other artist",
			session: media.MediaSession{AppID: "foobar2000.exe", Title: "Essence of the Forest", Artist: "Someone Else"},
			match:   false,
		},
	}

	for _, tc := range cases {
		if got := sameTrack(tc.session, api); got != tc.match {
			t.Errorf("%s: sameTrack = %v, want %v", tc.name, got, tc.match)
		}
	}
}
