// Package pins stores the user's pinned applications for the dock.
//
// # Why this is its own store
//
// An earlier version of the dock seeded its pinned list from the whole application
// index, which made it a wall of every installed program and told the user nothing
// about what they actually used. The dock then shipped showing only running windows,
// which is honest but not a dock.
//
// So the pins are explicit and persisted. The dock's running-window list stays live and
// unsaved, because that reflects reality; only what the user chose to keep is stored.
//
// # Identity
//
// A pin is keyed by Target, which is whatever launcher.Launch accepts: a .lnk path, an
// .exe path, or an AppUserModelID for a Store app. ProcName is stored alongside purely
// so the dock can mark a pin as running without resolving paths on every repaint.
//
// # Failure behaviour
//
// A corrupt or unreadable pins file yields an empty list rather than an error. A shell
// that cannot start because its dock is misconfigured is worse than a dock that
// appears to have nothing pinned. Writes are atomic, so the file is never left
// half-written.
package pins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// MaxPinCount bounds the store so a runaway widget cannot fill the disk or make the
// dock unusable. Well above any real dock.
const MaxPinCount = 128

// MaxTargetLen and MaxNameLen bound a single field, for the same reason.
const (
	MaxTargetLen = 2048
	MaxNameLen   = 256
)

// ErrLimitReached is returned when the store is already at MaxPinCount.
var ErrLimitReached = errors.New("too many pinned applications")

// Pin is one entry in the dock.
type Pin struct {
	// Target is what launcher.Launch is given. See the package comment on identity.
	Target string `json:"target"`
	// Name is the display label.
	Name string `json:"name"`
	// ProcName is the lowercase process name with no extension, used to decide whether
	// the pinned application is currently running.
	ProcName string `json:"proc_name,omitempty"`
}

// Key is the pin's identity within the store.
func (p Pin) Key() string {
	return strings.ToLower(strings.TrimSpace(p.Target))
}

// file is the on-disk shape. Versioned so a future format change can be detected rather
// than misread.
type file struct {
	Version int   `json:"version"`
	Pins    []Pin `json:"pins"`
}

// currentVersion is the only version this build writes.
const currentVersion = 1

// Store holds the pinned applications. Safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	path string
	pins []Pin

	// Degraded explains why the store may be incomplete, or is empty when healthy.
	//
	// Set once at load and then read-only, so it needs no lock. It is a field rather
	// than an Open return because the interesting case is "started fine but is not
	// showing your real pins", which is a state, not an event.
	Degraded string
}

// GetPath returns the pins file location, %APPDATA%\NovaBar\pins.json.
//
// Mirrors config.GetConfigPath so both live beside each other and are equally easy to
// find when someone needs to delete a setting by hand.
func GetPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot locate a config directory: %w", err)
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "NovaBar", "pins.json"), nil
}

// Open loads the store from path, or creates an empty one if the file does not exist.
//
// A file that exists but cannot be parsed is reported through the returned store's
// Degraded field rather than as an error, because refusing to start is the wrong
// response to a stray byte in a dock file.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("pins: empty path")
	}

	s := &Store{path: path}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil // first run
		}
		// Unreadable for another reason. Still usable, but the caller should know the
		// dock is not showing the user's real pins.
		s.Degraded = fmt.Sprintf("could not read pins: %v", err)
		return s, nil
	}

	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		s.Degraded = fmt.Sprintf("pins file is not valid JSON, starting empty: %v", err)
		return s, nil
	}

	for _, p := range f.Pins {
		if err := validate(p); err != nil {
			// Drop the bad entry and keep the rest rather than losing everything.
			s.Degraded = fmt.Sprintf("dropped one invalid pin: %v", err)
			continue
		}
		s.pins = append(s.pins, normalize(p))
	}

	return s, nil
}

// Path returns the file this store is backed by, empty if it has none.
func (s *Store) Path() string { return s.path }

// List returns the pins in user order.
func (s *Store) List() []Pin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Pin, len(s.pins))
	copy(out, s.pins)
	return out
}

// IsPinned reports whether a target is already pinned.
func (s *Store) IsPinned(target string) bool {
	key := Pin{Target: target}.Key()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexOfLocked(key) >= 0
}

// Add pins a target. Adding one that is already pinned is a no-op, not an error, so a
// double click does not surface a failure to the user.
func (s *Store) Add(p Pin) error {
	p = normalize(p)
	if err := validate(p); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := p.Key()
	if s.indexOfLocked(key) >= 0 {
		return nil
	}
	if len(s.pins) >= MaxPinCount {
		return ErrLimitReached
	}

	// Append rather than sort: dock order is the user's arrangement, and an alphabetical
	// pin list is not what anyone chose on purpose.
	s.pins = append(s.pins, p)
	return s.saveLocked()
}

// Remove unpins a target. Removing something that is not pinned is a no-op.
func (s *Store) Remove(target string) error {
	key := Pin{Target: target}.Key()

	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.indexOfLocked(key)
	if i < 0 {
		return nil
	}

	s.pins = append(s.pins[:i:i], s.pins[i+1:]...)
	return s.saveLocked()
}

// Toggle pins an unpinned target and unpins a pinned one, reporting the new state.
//
// This is what a pin button calls. Returning the resulting state avoids the caller
// having to re-read the list to know what it just did.
func (s *Store) Toggle(p Pin) (bool, error) {
	p = normalize(p)
	if err := validate(p); err != nil {
		return false, err
	}
	key := p.Key()

	s.mu.Lock()
	already := s.indexOfLocked(key)
	s.mu.Unlock()

	if already >= 0 {
		// Pass the original-cased target, not the folded key: lookup folds either way,
		// but handing back the lowercase key would quietly rewrite the stored path.
		return false, s.Remove(p.Target)
	}
	return true, s.Add(p)
}

// Move reorders a pin to a new index, for drag-to-arrange.
//
// Out-of-range indices are clamped rather than rejected: a drag that overshoots should
// still put the item somewhere sensible.
func (s *Store) Move(target string, to int) error {
	key := Pin{Target: target}.Key()

	s.mu.Lock()
	defer s.mu.Unlock()

	from := s.indexOfLocked(key)
	if from < 0 {
		return fmt.Errorf("pins: %q is not pinned", target)
	}

	if to < 0 {
		to = 0
	}
	if to >= len(s.pins) {
		to = len(s.pins) - 1
	}

	p := s.pins[from]
	rest := append(s.pins[:from:from], s.pins[from+1:]...)
	s.pins = append(rest[:to:to], append([]Pin{p}, rest[to:]...)...)
	return s.saveLocked()
}

// indexOfLocked finds a pin by key. Caller holds the lock.
func (s *Store) indexOfLocked(key string) int {
	for i, p := range s.pins {
		if p.Key() == key {
			return i
		}
	}
	return -1
}

// saveLocked writes the store atomically. Caller holds the lock.
//
// The temp file is created in the destination directory so the rename stays on one
// volume, which is what makes it atomic. Without that, a rename across volumes degrades
// to copy-then-delete and a crash mid-write truncates the file.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return errors.New("pins: store is not backed by a file")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pins: cannot create %s: %w", dir, err)
	}

	raw, err := json.MarshalIndent(file{Version: currentVersion, Pins: s.pins}, "", "  ")
	if err != nil {
		return fmt.Errorf("pins: cannot encode: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "pins-*.json")
	if err != nil {
		return fmt.Errorf("pins: cannot create a temporary file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup. If the rename succeeded this finds nothing to remove.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("pins: cannot write: %w", err)
	}
	// Flush to the device before the rename, or the rename can land before the data.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("pins: cannot flush: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pins: cannot close: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("pins: cannot replace %s: %w", s.path, err)
	}
	return nil
}

// validate rejects entries that could not be launched or displayed.
//
// Deliberately looser than launcher.ValidateTarget: that guards filesystem execution
// and would reject the AppUserModelID of a Store app, which is a legitimate pin. Launch
// time validation still applies; this only stops obviously broken rows from being stored.
func validate(p Pin) error {
	if strings.TrimSpace(p.Target) == "" {
		return errors.New("pin has no target")
	}
	if len(p.Target) > MaxTargetLen {
		return fmt.Errorf("pin target is %d bytes, limit is %d", len(p.Target), MaxTargetLen)
	}
	if len(p.Name) > MaxNameLen {
		return fmt.Errorf("pin name is %d bytes, limit is %d", len(p.Name), MaxNameLen)
	}

	// A NUL truncates the string when it reaches a Win32 call, and other control
	// characters only ever render as garbage in the dock.
	for _, r := range p.Target + p.Name {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return errors.New("pin contains a control character")
		}
	}

	if strings.TrimSpace(p.Name) == "" {
		return errors.New("pin has no name")
	}
	return nil
}

// normalize trims the fields and derives ProcName when the caller did not supply it.
func normalize(p Pin) Pin {
	p.Target = strings.TrimSpace(p.Target)
	p.Name = strings.TrimSpace(p.Name)
	if p.ProcName == "" {
		p.ProcName = DeriveProcName(p.Target)
	} else {
		p.ProcName = strings.ToLower(strings.TrimSpace(p.ProcName))
	}
	return p
}

// DeriveProcName returns the lowercase process name implied by a launch target.
//
// Mirrors what the dock widget does in JavaScript (`replace(/\.exe$/i, ”)`), so a pin
// created by the launcher and one created by the dock agree on what "running" means.
// The extension strip is case-insensitive for the same reason: on Windows `.EXE` and
// `.exe` are the same file, and a case-sensitive TrimSuffix would leave "thing.exe" as
// a process name that never matches anything.
func DeriveProcName(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	// An AUMID is "FamilyName!AppId" and has no file component to take.
	if !strings.ContainsAny(target, `/\`) {
		return ""
	}
	parts := strings.FieldsFunc(target, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return ""
	}

	name := parts[len(parts)-1]
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		name = name[:len(name)-4]
	}
	return strings.ToLower(name)
}
