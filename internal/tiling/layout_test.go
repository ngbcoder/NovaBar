package tiling

import "testing"

func TestLayoutColumns(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	gaps := Gaps{Outer: 10, Inner: 10}

	// 1 window
	r1 := LayoutColumns(bounds, 1, gaps)
	if len(r1) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(r1))
	}
	if r1[0].X != 10 || r1[0].Y != 10 || r1[0].Width != 1900 || r1[0].Height != 1060 {
		t.Errorf("unexpected rect for 1 window: %+v", r1[0])
	}

	// 2 windows
	r2 := LayoutColumns(bounds, 2, gaps)
	if len(r2) != 2 {
		t.Fatalf("expected 2 rects, got %d", len(r2))
	}
	// availableWidth = 1900 - 10 = 1890, split in 2 = 945 each
	if r2[0].Width != 945 || r2[1].Width != 945 {
		t.Errorf("expected 945 each, got %d and %d", r2[0].Width, r2[1].Width)
	}
	if r2[1].X != r2[0].X+r2[0].Width+gaps.Inner {
		t.Errorf("gap between columns incorrect: r1=%+v, r2=%+v", r2[0], r2[1])
	}
}

func TestLayoutBSP(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 0}

	rects := LayoutBSP(bounds, 4, gaps)
	if len(rects) != 4 {
		t.Fatalf("expected 4 rects, got %d", len(rects))
	}
	totalArea := 0
	for _, r := range rects {
		totalArea += r.Width * r.Height
	}
	if totalArea != 1000*1000 {
		t.Errorf("total area mismatch: expected 1000000, got %d", totalArea)
	}
}

func TestLayoutRows(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 600}
	gaps := Gaps{Outer: 0, Inner: 0}

	rects := LayoutRows(bounds, 3, gaps)
	if len(rects) != 3 {
		t.Fatalf("expected 3 rects, got %d", len(rects))
	}
	for i, r := range rects {
		if r.Height != 200 {
			t.Errorf("expected height 200 for index %d, got %d", i, r.Height)
		}
	}
}

// TestLayoutBSPWithGaps checks that the inner gap is subtracted exactly once per split.
// A naive implementation double-counts the gap at nested levels and windows end up
// overlapping by the gap amount.
func TestLayoutBSPWithGaps(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 10}

	rects := LayoutBSP(bounds, 4, gaps)
	if len(rects) != 4 {
		t.Fatalf("expected 4 rects, got %d", len(rects))
	}

	// 1000 wide split vertically first: gap once.
	left := rects[0]
	right := rects[2]
	if right.X != left.X+left.Width+gaps.Inner {
		t.Errorf("vertical split gap wrong: left=%+v right=%+v want right.X=%d",
			left, right, left.X+left.Width+gaps.Inner)
	}

	// Total consumed area must be bounds minus the gaps actually inserted.
	// 3 internal splits (4 leaves => 3 nodes) each consuming one gap of 10.
	area := 0
	for _, r := range rects {
		area += r.Width * r.Height
	}
	if area >= bounds.Width*bounds.Height {
		t.Errorf("gap area not removed: %d >= %d", area, bounds.Width*bounds.Height)
	}
}

// TestLayoutZeroCount pins the guard that keeps an empty window list from producing a
// panic in the layout callers.
func TestLayoutZeroCount(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	gaps := Gaps{Outer: 8, Inner: 8}

	layouts := map[string]func(Rect, int, Gaps) []Rect{
		"bsp":     LayoutBSP,
		"columns": LayoutColumns,
		"rows":    LayoutRows,
		"grid":    LayoutGrid,
		"monocle": LayoutMonocle,
	}

	for name, fn := range layouts {
		for _, count := range []int{0, -1} {
			got := fn(bounds, count, gaps)
			if got != nil {
				t.Errorf("%s(%d) = %v, want nil", name, count, got)
			}
		}
	}
}

// TestLayoutNonZeroOrigin verifies layouts honour an offset work area, which is the
// normal case on a secondary monitor or with a taskbar on the left.
func TestLayoutNonZeroOrigin(t *testing.T) {
	bounds := Rect{X: 1920, Y: 40, Width: 1280, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 0}

	for name, fn := range map[string]func(Rect, int, Gaps) []Rect{
		"bsp":     LayoutBSP,
		"columns": LayoutColumns,
		"rows":    LayoutRows,
		"grid":    LayoutGrid,
		"monocle": LayoutMonocle,
	} {
		rects := fn(bounds, 4, gaps)
		for i, r := range rects {
			if r.X < bounds.X || r.Y < bounds.Y {
				t.Errorf("%s: rect %d %+v escapes the work area origin %+v", name, i, r, bounds)
			}
			if r.X+r.Width > bounds.X+bounds.Width {
				t.Errorf("%s: rect %d %+v overflows the work area right edge", name, i, r)
			}
		}
	}
}

// TestLayoutGapsExceedBounds verifies that a gap larger than the area clamps to zero
// size rather than producing negative extents.
func TestLayoutGapsExceedBounds(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 20, Height: 20}
	gaps := Gaps{Outer: 50, Inner: 50}

	for name, fn := range map[string]func(Rect, int, Gaps) []Rect{
		"bsp":     LayoutBSP,
		"columns": LayoutColumns,
		"rows":    LayoutRows,
		"grid":    LayoutGrid,
		"monocle": LayoutMonocle,
	} {
		rects := fn(bounds, 3, gaps)
		for i, r := range rects {
			if r.Width < 0 || r.Height < 0 {
				t.Errorf("%s: rect %d %+v has negative extents", name, i, r)
			}
		}
	}
}

func TestLayoutMonocle(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1600, Height: 900}
	gaps := Gaps{Outer: 20, Inner: 8}

	rects := LayoutMonocle(bounds, 3, gaps)
	if len(rects) != 3 {
		t.Fatalf("expected 3 rects, got %d", len(rects))
	}

	want := Rect{X: 20, Y: 20, Width: 1560, Height: 860}
	for i, r := range rects {
		if r != want {
			t.Errorf("monocle rect %d = %+v, want %+v", i, r, want)
		}
	}
}

func TestLayoutGrid(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 0}

	// Explicit 2x2.
	rects := LayoutGridCols(bounds, 4, gaps, 2)
	if len(rects) != 4 {
		t.Fatalf("expected 4 rects, got %d", len(rects))
	}
	want := [4]Rect{
		{X: 0, Y: 0, Width: 500, Height: 500},
		{X: 500, Y: 0, Width: 500, Height: 500},
		{X: 0, Y: 500, Width: 500, Height: 500},
		{X: 500, Y: 500, Width: 500, Height: 500},
	}
	for i := range want {
		if rects[i] != want[i] {
			t.Errorf("grid rect %d = %+v, want %+v", i, rects[i], want[i])
		}
	}
}

// TestLayoutGridClampsColumns covers a column count above the window count, which would
// otherwise emit empty columns and leave the real windows squeezed into one row.
func TestLayoutGridClampsColumns(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 0}

	rects := LayoutGridCols(bounds, 2, gaps, 8)
	if len(rects) != 2 {
		t.Fatalf("expected 2 rects, got %d", len(rects))
	}
	// Clamped to 2 columns => each should span the full width.
	for i, r := range rects {
		if r.Width != 500 {
			t.Errorf("rect %d width = %d, want 500", i, r.Width)
		}
	}
}

// TestLayoutGridAutoColumns checks the auto column count keeps cells roughly square on
// a widescreen, which is the whole point of choosing it automatically.
func TestLayoutGridAutoColumns(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 2560, Height: 1440}
	gaps := Gaps{Outer: 0, Inner: 0}

	rects := LayoutGrid(bounds, 4, gaps)
	if len(rects) != 4 {
		t.Fatalf("expected 4 rects, got %d", len(rects))
	}

	aspect := func(r Rect) float64 {
		return float64(r.Width) / float64(r.Height)
	}

	for i, r := range rects {
		a := aspect(r)
		if a < 0.5 || a > 2.0 {
			t.Errorf("rect %d %+v has aspect %.2f, want between 0.5 and 2.0", i, r, a)
		}
	}
}

// TestLayoutGridUnevenCount covers a count that does not fill the last row, which is
// the normal case as windows open and close.
func TestLayoutGridUnevenCount(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1000, Height: 1000}
	gaps := Gaps{Outer: 0, Inner: 0}

	rects := LayoutGridCols(bounds, 5, gaps, 3)
	if len(rects) != 5 {
		t.Fatalf("expected 5 rects, got %d", len(rects))
	}

	// Rows: ceil(5/3) = 2. First three on row 0, two on row 1.
	if rects[0].Y != rects[1].Y || rects[0].Y != rects[2].Y {
		t.Errorf("first three rects should share a row: %+v %+v %+v", rects[0], rects[1], rects[2])
	}
	if rects[3].Y != rects[4].Y {
		t.Errorf("last two rects should share a row: %+v %+v", rects[3], rects[4])
	}
	if rects[3].Y == rects[0].Y {
		t.Errorf("row 1 should be below row 0: %+v vs %+v", rects[0], rects[3])
	}
}