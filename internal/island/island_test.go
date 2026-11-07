package island

import (
	"testing"
	"time"
)

func TestPriorityOrder(t *testing.T) {
	if !(PriorityMedia < PriorityNotification && PriorityNotification < PriorityUserAction) {
		t.Fatalf("priority order wrong: %v %v %v", PriorityMedia, PriorityNotification, PriorityUserAction)
	}
	if PriorityMedia.String() != "media" {
		t.Fatalf("unexpected string: %s", PriorityMedia.String())
	}
}

func TestPresentAndCurrent(t *testing.T) {
	c := New()
	if c.Current() != nil {
		t.Fatal("new controller should be empty")
	}
	ok := c.Present(Item{ID: "m1", Priority: PriorityMedia, Title: "track"})
	if !ok {
		t.Fatal("present should succeed on empty island")
	}
	cur := c.Current()
	if cur == nil || cur.ID != "m1" {
		t.Fatalf("current wrong: %+v", cur)
	}
}

func TestHigherPriorityReplacesAndRestores(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	ok := c.Present(Item{ID: "n1", Priority: PriorityNotification})
	if !ok {
		t.Fatal("notification should replace media")
	}
	if c.Current().ID != "n1" {
		t.Fatalf("current = %s", c.Current().ID)
	}
	if c.Len() != 1 {
		t.Fatalf("stack len = %d", c.Len())
	}
	// Dismiss notification restores media.
	if !c.Dismiss("n1") {
		t.Fatal("dismiss failed")
	}
	if c.Current() == nil || c.Current().ID != "m1" {
		t.Fatalf("restore failed: %+v", c.Current())
	}
	if c.Len() != 0 {
		t.Fatalf("stack not empty: %d", c.Len())
	}
}

func TestLowerPriorityRejected(t *testing.T) {
	c := New()
	c.Present(Item{ID: "n1", Priority: PriorityNotification})
	ok := c.Present(Item{ID: "m1", Priority: PriorityMedia})
	if ok {
		t.Fatal("media should not replace notification")
	}
	if c.Current().ID != "n1" {
		t.Fatalf("current changed: %s", c.Current().ID)
	}
	if c.Len() != 0 {
		t.Fatalf("stack changed: %d", c.Len())
	}
}

func TestEqualPriorityReplaces(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	ok := c.Present(Item{ID: "m2", Priority: PriorityMedia})
	if !ok {
		t.Fatal("equal priority should replace")
	}
	if c.Current().ID != "m2" {
		t.Fatalf("current = %s", c.Current().ID)
	}
	if c.Len() != 1 || c.Stack()[0].ID != "m1" {
		t.Fatalf("previous not stacked")
	}
}

func TestDismissUnknown(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	if c.Dismiss("nope") {
		t.Fatal("dismissing unknown should be false")
	}
	if c.Current().ID != "m1" {
		t.Fatal("current changed")
	}
}

func TestDismissCurrentRestoresChain(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	c.Present(Item{ID: "n1", Priority: PriorityNotification})
	c.Present(Item{ID: "u1", Priority: PriorityUserAction})
	// dismiss user action -> notification
	c.DismissCurrent()
	if c.Current().ID != "n1" {
		t.Fatalf("got %s", c.Current().ID)
	}
	// dismiss notification -> media
	c.DismissCurrent()
	if c.Current().ID != "m1" {
		t.Fatalf("got %s", c.Current().ID)
	}
	// dismiss media -> empty
	c.DismissCurrent()
	if c.Current() != nil {
		t.Fatalf("expected empty, got %+v", c.Current())
	}
}

func TestRestore(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	c.Present(Item{ID: "n1", Priority: PriorityNotification})
	// Restore discards current and pops stack.
	if !c.Restore() {
		t.Fatal("restore failed")
	}
	if c.Current().ID != "m1" {
		t.Fatalf("got %s", c.Current().ID)
	}
}

func TestDismissFromStack(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	c.Present(Item{ID: "n1", Priority: PriorityNotification})
	// dismiss the stacked (non-current) media by id should remove it without restoring
	// notification? Current remains notification, stack empty.
	if !c.Dismiss("m1") {
		t.Fatal("dismiss from stack failed")
	}
	if c.Current().ID != "n1" {
		t.Fatal("current changed")
	}
	if c.Len() != 0 {
		t.Fatalf("stack len = %d", c.Len())
	}
}

func TestCanInterrupt(t *testing.T) {
	c := New()
	c.Present(Item{ID: "n1", Priority: PriorityNotification})
	if c.CanInterrupt(PriorityMedia) {
		t.Fatal("media should not interrupt notification")
	}
	if !c.CanInterrupt(PriorityUserAction) {
		t.Fatal("user action should interrupt")
	}
}

func TestPruneExpired(t *testing.T) {
	now := time.Now()
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia, ExpiresAt: now.Add(-time.Second)})
	c.Present(Item{ID: "n1", Priority: PriorityNotification}) // replaces, stacks expired m1
	// prune: removes expired m1 from stack
	removed := c.PruneExpired(now)
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if c.Len() != 0 {
		t.Fatalf("stack len = %d", c.Len())
	}
}

func TestEmptyAndClear(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	c.Clear()
	if !c.Empty() {
		t.Fatal("not empty after clear")
	}
	if c.Current() != nil {
		t.Fatal("current not nil")
	}
}

func TestDismissEmptyIDCurrent(t *testing.T) {
	c := New()
	c.Present(Item{ID: "m1", Priority: PriorityMedia})
	if !c.Dismiss("") {
		t.Fatal("empty id should dismiss current")
	}
	if c.Current() != nil {
		t.Fatal("not dismissed")
	}
}

func TestAliases(t *testing.T) {
	c := NewController()
	if c == nil {
		t.Fatal("NewController nil")
	}
	c.Show(Item{ID: "a", Priority: PriorityLow})
	if c.Peek().ID != "a" {
		t.Fatal("alias Peek failed")
	}
	c.Hide("a")
	if c.Active() != nil {
		t.Fatal("alias Hide failed")
	}
}
func TestConcurrency(t *testing.T) {
	c := New()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			for j := 0; j < 100; j++ {
				c.Present(Item{ID: "x", Priority: PriorityMedia})
				c.Current()
				c.Dismiss("x")
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
