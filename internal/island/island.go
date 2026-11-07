// Package island models the Dynamic Island's interruptible content.
//
// The island is one surface that many things want to use at once: a media track
// playing, a notification arriving, a command waiting for input. If every caller
// just overwrote the previous content, a notification would clobber a track and
// never give it back. This package is the rule for that: content carries a
// priority, and a higher-priority item may temporarily replace a lower-priority
// one. When the higher-priority item is dismissed, the previous item is
// restored.
//
// Priority order, lowest to highest:
//
//	PriorityMedia < PriorityNotification < PriorityUserAction
//
// The package is deliberately free of UI, RPC and daemon dependencies so the
// rule can be tested in isolation and reused by any caller.
package island

import (
	"errors"
	"sync"
	"time"
)

// Priority is the interruptibility level of an island item.
type Priority int

const (
	// PriorityMedia is the lowest level: a playing track or other ambient state.
	PriorityMedia Priority = iota
	// PriorityNotification is the middle level: a transient notification.
	PriorityNotification
	// PriorityUserAction is the highest level: something waiting on the user.
	PriorityUserAction
)

// PriorityLevel is an alias for Priority, for callers that prefer the word
// "level".
type PriorityLevel = Priority

const (
	LevelMedia        = PriorityMedia
	LevelNotification = PriorityNotification
	LevelUserAction   = PriorityUserAction
)

// Generic aliases for callers that think in low/medium/high terms.
const (
	PriorityLow    = PriorityMedia
	PriorityMedium = PriorityNotification
	PriorityHigh   = PriorityUserAction
)

// String renders a priority as a stable, lowercase identifier.
func (p Priority) String() string {
	switch p {
	case PriorityMedia:
		return "media"
	case PriorityNotification:
		return "notification"
	case PriorityUserAction:
		return "user_action"
	default:
		return "unknown"
	}
}

// Less reports whether p is strictly lower priority than other.
func (p Priority) Less(other Priority) bool { return p < other }

// Item is one piece of content that can occupy the island.
type Item struct {
	// ID identifies the item within its kind. Empty IDs are allowed but make
	// dismissal by ID impossible; such items can still be dismissed as current.
	ID string `json:"id,omitempty"`

	// Priority is the interruptibility level of this item.
	Priority Priority `json:"priority"`

	// Kind is a short label for the content type, e.g. "media", "notification".
	Kind string `json:"kind,omitempty"`

	// Title and Body are the human-readable content.
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`

	// Source identifies where the item came from, e.g. "gsmtc", "spotify".
	Source string `json:"source,omitempty"`

	// Payload carries kind-specific data for the renderer.
	Payload map[string]any `json:"payload,omitempty"`

	// CreatedAt is when the item was presented. Zero means "now" at present time.
	CreatedAt time.Time `json:"created_at,omitempty"`

	// ExpiresAt, when non-zero, marks the item as expired after that time.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Content, Entry and IslandItem are aliases for Item.
type (
	Content    = Item
	Entry      = Item
	IslandItem = Item
)

// Kind constants for the content the shell knows about.
const (
	KindMedia        = "media"
	KindNotification = "notification"
	KindUserAction   = "user_action"
)

// Expired reports whether the item has passed its expiry time at now.
func (it Item) Expired(now time.Time) bool {
	if it.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(it.ExpiresAt)
}

// Controller is the interruptible island state.
//
// It holds at most one current item. Presenting a higher-or-equal priority
// item pushes the current item onto a stack and replaces it. Dismissing the
// current item pops the stack and restores whatever was there before.
type Controller struct {
	mu      sync.Mutex
	current *Item
	stack   []Item
}

// New creates an empty controller.
func New() *Controller { return &Controller{} }

// NewController, NewIsland, NewManager and NewStack are aliases for New.
func NewController() *Controller { return New() }
func NewIsland() *Controller     { return New() }
func NewManager() *Controller    { return New() }
func NewStack() *Controller      { return New() }

// Present places item on the island.
//
// An item replaces the current one when there is no current item, or when its
// priority is greater than or equal to the current item's priority. A lower
// priority item is rejected and the island is left untouched. The previous
// current item, if any, is pushed onto the restore stack.
//
// Returns true when the item was accepted.
func (c *Controller) Present(item Item) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}

	if c.current != nil && item.Priority < c.current.Priority {
		return false
	}

	if c.current != nil {
		c.stack = append(c.stack, *c.current)
	}
	cp := item
	c.current = &cp
	return true
}

// Show, Push, Display, Add, Interrupt and Replace are aliases for Present.
func (c *Controller) Show(item Item) bool      { return c.Present(item) }
func (c *Controller) Push(item Item) bool      { return c.Present(item) }
func (c *Controller) Display(item Item) bool   { return c.Present(item) }
func (c *Controller) Add(item Item) bool       { return c.Present(item) }
func (c *Controller) Interrupt(item Item) bool { return c.Present(item) }
func (c *Controller) Replace(item Item) bool   { return c.Present(item) }

// Current returns the item currently shown, or nil when the island is empty.
func (c *Controller) Current() *Item {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil
	}
	cp := *c.current
	return &cp
}

// Peek, Top, Active and Get are aliases for Current.
func (c *Controller) Peek() *Item   { return c.Current() }
func (c *Controller) Top() *Item    { return c.Current() }
func (c *Controller) Active() *Item { return c.Current() }
func (c *Controller) Get() *Item    { return c.Current() }

// CanInterrupt reports whether candidate may replace current.
func (c *Controller) CanInterrupt(candidate Priority) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return true
	}
	return candidate >= c.current.Priority
}

// MayInterrupt and Allows are aliases for CanInterrupt.
func (c *Controller) MayInterrupt(candidate Priority) bool { return c.CanInterrupt(candidate) }
func (c *Controller) Allows(candidate Priority) bool       { return c.CanInterrupt(candidate) }

// Dismiss removes the item with the given ID from the island.
//
// If the dismissed item is the current one, the previous item is restored from
// the stack. Dismissing an item that is not current removes it from the stack
// so it can never be restored later. Returns true when an item was removed.
func (c *Controller) Dismiss(id string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.current != nil && (id == "" || c.current.ID == id) {
		c.current = c.popStack()
		return true
	}

	// Remove from the stack without restoring.
	for i := len(c.stack) - 1; i >= 0; i-- {
		if c.stack[i].ID == id {
			c.stack = append(c.stack[:i], c.stack[i+1:]...)
			return true
		}
	}
	return false
}

// Close, Remove, Hide and Delete are aliases for Dismiss.
func (c *Controller) Close(id string) bool  { return c.Dismiss(id) }
func (c *Controller) Remove(id string) bool { return c.Dismiss(id) }
func (c *Controller) Hide(id string) bool   { return c.Dismiss(id) }
func (c *Controller) Delete(id string) bool { return c.Dismiss(id) }

// DismissCurrent dismisses whatever is currently shown, restoring the previous
// item. Returns true when something was dismissed.
func (c *Controller) DismissCurrent() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return false
	}
	c.current = c.popStack()
	return true
}

// Restore pops one item off the restore stack and makes it current, discarding
// the current item. Returns true when an item was restored.
func (c *Controller) Restore() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.stack) == 0 {
		// Nothing to restore to; clear the current item instead of keeping a
		// dismissed one alive.
		c.current = nil
		return false
	}
	c.current = c.popStack()
	return true
}

// Pop is an alias for Restore.
func (c *Controller) Pop() bool { return c.Restore() }

// Undo and Revert are aliases for Restore.
func (c *Controller) Undo() bool   { return c.Restore() }
func (c *Controller) Revert() bool { return c.Restore() }

// Clear empties the island and its restore stack.
func (c *Controller) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = nil
	c.stack = nil
}

// Reset is an alias for Clear.
func (c *Controller) Reset() { c.Clear() }

// Stack returns a copy of the restore stack, oldest first.
func (c *Controller) Stack() []Item {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Item, len(c.stack))
	copy(out, c.stack)
	return out
}

// Items, History and Pending are aliases for Stack.
func (c *Controller) Items() []Item   { return c.Stack() }
func (c *Controller) History() []Item { return c.Stack() }
func (c *Controller) Pending() []Item { return c.Stack() }

// Len reports the number of items waiting to be restored (excludes current).
func (c *Controller) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.stack)
}

// Empty reports whether nothing is currently shown and nothing is waiting.
func (c *Controller) Empty() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current == nil && len(c.stack) == 0
}

// PruneExpired removes expired items from the stack and, if the current item
// has expired, dismisses it and restores the previous one. now is the time to
// test against; a zero value uses time.Now.
//
// Returns the number of items removed.
func (c *Controller) PruneExpired(now time.Time) int {
	if c == nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0

	// Filter the stack.
	kept := c.stack[:0:0]
	for _, it := range c.stack {
		if it.Expired(now) {
			removed++
			continue
		}
		kept = append(kept, it)
	}
	c.stack = kept

	if c.current != nil && c.current.Expired(now) {
		c.current = c.popStackLocked()
		removed++
	}
	return removed
}

// popStack removes and returns the top of the stack, or nil.
func (c *Controller) popStack() *Item {
	return c.popStackLocked()
}

func (c *Controller) popStackLocked() *Item {
	if len(c.stack) == 0 {
		return nil
	}
	last := c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
	return &last
}

var ErrEmpty = errors.New("island: no current item")
