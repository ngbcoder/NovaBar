package daemon

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/ngbcoder/NovaBar/internal/media"
	"github.com/ngbcoder/NovaBar/internal/spotify"
)

// spotifySession converts a Spotify track into the shell's session shape.
func spotifySession(tr spotify.Track) media.MediaSession {
	playback := media.StatePaused
	if tr.IsPlaying {
		playback = media.StatePlaying
	}
	return media.MediaSession{
		AppID:       "Spotify.exe",
		Title:       tr.Name,
		Artist:      strings.Join(tr.Artists, ", "),
		Album:       tr.Album,
		Artwork:     tr.Artwork,
		Playback:    playback,
		CanPlay:     true,
		CanPause:    true,
		CanNext:     true,
		CanPrevious: true,
		Source:      "spotify",
	}
}

// spotifyReady reports whether the Spotify Web API can be asked for a track.
func (d *daemon) spotifyReady() bool {
	client := d.spotifyClient()
	return d.cfg.Spotify.Enabled && client != nil && client.IsConnected()
}

// currentMediaSession returns the session the UI should display.
//
// There are two possible sources and they must not be allowed to disagree. GSMTC is
// the local machine's view: it knows whatever a Windows player is doing right now.
// The Spotify Web API also sees playback happening on another device, which GSMTC
// cannot. So Spotify wins when it is preferred and has a track to report, and it is
// also the fallback when GSMTC has nothing usable to show.
//
// Every path that reports media - media.get, the media.state broadcast, and the
// session logged after a transport command - goes through here. They used to pick
// their own source, which is why the bar could show one player and then be pushed
// updates about another.
func (d *daemon) currentMediaSession() media.MediaSession {
	local := media.MediaSession{Playback: media.StateUnknown, Source: "none"}
	if d.media != nil {
		local = d.media.GetCurrentSession()
	}

	if !d.spotifyReady() {
		return local
	}
	client := d.spotifyClient()
	if client == nil {
		return local
	}

	// Remote Spotify playback is invisible to GSMTC, but a local player GSMTC can
	// see is the better answer unless the user asked Spotify to win outright.
	if !d.cfg.Spotify.Prefer && mediaSessionUsable(local) {
		return d.withSpotifyArtwork(local)
	}

	if tr, err := client.CurrentTrack(context.Background()); err == nil {
		return spotifySession(tr)
	} else {
		d.spotifyErrOnce.Do(func() {
			log.Printf("[NovaBar Media] Spotify current-track failed: %v", err)
		})
	}
	return d.withSpotifyArtwork(local)
}

// withSpotifyArtwork borrows album art for a session GSMTC is reporting.
//
// GSMTC gives title, artist and album but no image. Pulling thumbnails out of WinRT
// means raw streams and IBuffer handling, which is a lot of surface to add for a 24px
// square, so the cover is asked for over the Web API instead.
//
// The player is not required to be the Spotify app: the desktop client is not the only
// way to hear Spotify, and the web player reports itself as the browser that hosts it
// ("Chrome", "Firefox", ...). What is required is that the Web API describes the same
// track, because attaching the API's cover to whatever else happens to be playing
// locally would label the user's music with someone else's album.
//
// The lookup is remembered per track. A media poll runs every couple of seconds and
// the answer cannot change while the track plays, so refetching would spend the user's
// rate limit on an image that is already on screen. A miss is remembered only briefly,
// since a miss usually means "the API has not caught up yet", not "never".
func (d *daemon) withSpotifyArtwork(s media.MediaSession) media.MediaSession {
	if s.Artwork != "" || !mediaSessionUsable(s) || !d.spotifyReady() {
		return s
	}

	key := s.Title + "\x00" + s.Artist + "\x00" + s.Album

	d.artworkMu.Lock()
	if d.artworkKey == key && (d.artworkURL != "" || time.Since(d.artworkFetched) < artworkMissTTL) {
		s.Artwork = d.artworkURL
		d.artworkMu.Unlock()
		return s
	}
	d.artworkMu.Unlock()

	client := d.spotifyClient()
	if client == nil {
		return s
	}

	artwork := ""
	if tr, err := client.CurrentTrack(context.Background()); err == nil && sameTrack(s, tr) {
		artwork = tr.Artwork
	}

	d.artworkMu.Lock()
	d.artworkKey = key
	d.artworkURL = artwork
	d.artworkFetched = time.Now()
	d.artworkMu.Unlock()

	s.Artwork = artwork
	return s
}

// artworkMissTTL is how long a failed artwork lookup is trusted.
const artworkMissTTL = 30 * time.Second

// sameTrack reports whether a session GSMTC is reporting describes the track the Web
// API just reported.
//
// Only the title and the artists are compared. The album, duration and identifiers are
// reported differently by different players, and GSMTC regularly reports just the first
// artist of a collaboration, so any one of them would reject a real match.
func sameTrack(s media.MediaSession, tr spotify.Track) bool {
	if !strings.EqualFold(strings.TrimSpace(s.Title), strings.TrimSpace(tr.Name)) {
		return false
	}

	// A session with no artists is matched on its title alone: there is nothing else
	// to check, and a title collision between two players is rare enough to accept.
	for _, name := range strings.Split(s.Artist, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := false
		for _, artist := range tr.Artists {
			if strings.EqualFold(strings.TrimSpace(artist), name) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// mediaSessionUsable reports whether a session has something worth showing.
func mediaSessionUsable(s media.MediaSession) bool {
	return s.Source != "" && s.Source != "none" && s.Title != ""
}

// resolveSpotifyCommand maps a UI transport command onto the Web API call to make.
//
// Toggle has to be resolved against the session the user is looking at. Sending Play
// for every toggle left the island's pause button unable to pause: it looked like a
// pause button, but the daemon asked Spotify to keep playing.
func resolveSpotifyCommand(cmd media.TransportCommand, session media.MediaSession) media.TransportCommand {
	switch cmd {
	case media.CommandTogglePlay:
		if session.Playback == media.StatePlaying {
			return media.CommandPause
		}
		return media.CommandPlay
	case media.CommandStop:
		// The Web API has no stop; pausing is the closest equivalent.
		return media.CommandPause
	}
	return cmd
}

// refreshCurrentMedia re-reads GSMTC and returns the session to display.
//
// The media pump calls this rather than reading the controller directly, so the
// session it compares on one tick is the same one media.get would have returned.
func (d *daemon) refreshCurrentMedia() media.MediaSession {
	if d.media != nil {
		// A refresh that fails is usually "nothing is playing", and the controller
		// clears the session in that case.
		_ = d.media.Refresh()
	}
	return d.currentMediaSession()
}
