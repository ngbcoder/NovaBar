// Package spotify wraps the Spotify Web API for NovaBar.
//
// Authentication uses OAuth2 with PKCE, so no client secret is required. Tokens are
// stored under %APPDATA%\NovaBar so the user only has to connect once.
package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	authURL  = "https://accounts.spotify.com/authorize"
	tokenURL = "https://accounts.spotify.com/api/token"
	apiBase  = "https://api.spotify.com/v1"

	scope = "user-read-playback-state user-read-currently-playing user-modify-playback-state"
)

// Track is the metadata for one Spotify track.
type Track struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Artists   []string `json:"artists"`
	Album     string   `json:"album"`
	Artwork   string   `json:"artwork,omitempty"`
	IsPlaying bool     `json:"is_playing"`
}

// albumImage is one entry of an album's image list.
type albumImage struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

// artworkURL picks an album image from the API's list.
//
// The list arrives widest first, so the smallest image that is still comfortably
// larger than the thumbnail is the cheapest one that will not look soft on a
// high-density display. Falls back to whatever is there rather than to nothing.
func artworkURL(images []albumImage) string {
	best := ""
	bestWidth := 0
	for _, img := range images {
		if img.URL == "" {
			continue
		}
		if best == "" {
			best = img.URL
			bestWidth = img.Width
			continue
		}
		if img.Width >= 128 && (bestWidth < 128 || img.Width < bestWidth) {
			best = img.URL
			bestWidth = img.Width
		}
	}
	return best
}

// Token holds the OAuth2 credentials.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	Expiry       time.Time `json:"expiry"`
}

// Client is a Spotify Web API client.
type Client struct {
	clientID   string
	httpClient *http.Client

	mu       sync.Mutex
	token    Token
	verifier string
	state    string

	redirectURI string
}

// New creates a client and loads any stored token.
func New(clientID string) *Client {
	c := &Client{
		clientID:   clientID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	_ = c.loadToken()
	return c
}

// TokenPath returns where tokens are stored.
func TokenPath() (string, error) {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, "NovaBar", "spotify.json"), nil
}

func (c *Client) loadToken() error {
	path, err := TokenPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	c.mu.Lock()
	c.token = t
	c.mu.Unlock()
	return nil
}

func (c *Client) saveToken() error {
	path, err := TokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	c.mu.Lock()
	data, err := json.MarshalIndent(c.token, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// IsConnected reports whether a token is present.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token.AccessToken != ""
}

// Disconnect forgets the stored credentials.
//
// Removing the file rather than writing an empty token means the next start cannot
// half-load a credential from a previous account.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	c.token = Token{}
	c.mu.Unlock()

	path, err := TokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AuthURL returns the URL the user should open to connect Spotify.
func (c *Client) AuthURL(redirectURI string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.redirectURI = redirectURI
	c.state = randomString(16)
	c.verifier = randomString(64)

	challenge := pkceChallenge(c.verifier)

	u, _ := url.Parse(authURL)
	q := u.Query()
	q.Set("client_id", c.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scope)
	q.Set("state", c.state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String()
}

// Exchange trades an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, redirectURI, code, state string) error {
	c.mu.Lock()
	if state != c.state || c.verifier == "" {
		c.mu.Unlock()
		return fmt.Errorf("invalid oauth state")
	}
	verifier := c.verifier
	c.redirectURI = redirectURI
	c.mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", c.clientID)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token exchange failed: %s", string(body))
	}

	var t Token
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	t.Expiry = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	c.mu.Lock()
	c.token = t
	c.mu.Unlock()
	return c.saveToken()
}

// ensureToken refreshes the token if needed.
func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok.AccessToken == "" {
		return fmt.Errorf("not connected")
	}
	if time.Now().Before(tok.Expiry.Add(-30 * time.Second)) {
		return nil
	}
	if tok.RefreshToken == "" {
		return fmt.Errorf("no refresh token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tok.RefreshToken)
	form.Set("client_id", c.clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed: %s", string(body))
	}
	var nt Token
	if err := json.NewDecoder(resp.Body).Decode(&nt); err != nil {
		return err
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = tok.RefreshToken
	}
	nt.Expiry = time.Now().Add(time.Duration(nt.ExpiresIn) * time.Second)
	c.mu.Lock()
	c.token = nt
	c.mu.Unlock()
	return c.saveToken()
}

// CurrentTrack returns the currently playing track.
func (c *Client) CurrentTrack(ctx context.Context) (Track, error) {
	if err := c.ensureToken(ctx); err != nil {
		return Track{}, err
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, "/me/player/currently-playing", nil)
	if err != nil {
		return Track{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Track{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return Track{}, fmt.Errorf("no track playing")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Track{}, fmt.Errorf("currently-playing failed: %s", string(body))
	}
	var payload struct {
		IsPlaying bool `json:"is_playing"`
		Item      struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Album struct {
				Name   string       `json:"name"`
				Images []albumImage `json:"images"`
			} `json:"album"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Track{}, err
	}
	tr := Track{
		ID:        payload.Item.ID,
		Name:      payload.Item.Name,
		Album:     payload.Item.Album.Name,
		Artwork:   artworkURL(payload.Item.Album.Images),
		IsPlaying: payload.IsPlaying,
	}
	for _, a := range payload.Item.Artists {
		tr.Artists = append(tr.Artists, a.Name)
	}
	return tr, nil
}

// Queue returns the upcoming tracks.
func (c *Client) Queue(ctx context.Context) ([]Track, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, "/me/player/queue", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue failed: %s", string(body))
	}
	var payload struct {
		Queue []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Album struct {
				Name string `json:"name"`
			} `json:"album"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"queue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(payload.Queue))
	for _, q := range payload.Queue {
		tr := Track{ID: q.ID, Name: q.Name, Album: q.Album.Name}
		for _, a := range q.Artists {
			tr.Artists = append(tr.Artists, a.Name)
		}
		out = append(out, tr)
	}
	return out, nil
}

// Play resumes playback.
func (c *Client) Play(ctx context.Context) error {
	return c.simpleCommand(ctx, http.MethodPut, "/me/player/play")
}

// Pause pauses playback.
func (c *Client) Pause(ctx context.Context) error {
	return c.simpleCommand(ctx, http.MethodPut, "/me/player/pause")
}

// Next skips to the next track.
func (c *Client) Next(ctx context.Context) error {
	return c.simpleCommand(ctx, http.MethodPost, "/me/player/next")
}

// Previous skips to the previous track.
func (c *Client) Previous(ctx context.Context) error {
	return c.simpleCommand(ctx, http.MethodPost, "/me/player/previous")
}

func (c *Client) simpleCommand(ctx context.Context, method, path string) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	req, err := c.newAPIRequest(ctx, method, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s failed: %s", path, string(body))
	}
	return nil
}

func (c *Client) newAPIRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	c.mu.Lock()
	tok := c.token.AccessToken
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func randomString(n int) string {
	size := (n*3 + 3) / 4
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
