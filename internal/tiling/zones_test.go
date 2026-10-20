package tiling

import "testing"

// TestLayoutZonesPutsTheFirstWindowInTheChosenCell is the check that a picked cell
// is the cell the window ends up in.
//
// The caller puts the window that is being moved first, so the reserved cell has to
// belong to it. A layout that spread the windows out and gave the reserved cell to
// whoever was last would make the picker pick a zone for a window that had not
// moved.
func TestLayoutZonesPutsTheFirstWindowInTheChosenCell(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	gaps := Gaps{Outer: 0, Inner: 0}

	cells := ZoneCellRects(bounds, 9, gaps)

	for _, cell := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8} {
		rects := LayoutZones(bounds, 9, gaps, cell)
		if len(rects) != 9 {
			t.Fatalf("cell %d: got %d rects, want 9", cell, len(rects))
		}

		// The first rect is the chosen cell, and every other rect is a different
		// cell: no two windows may share one.
		seen := map[Rect]bool{}
		for i, r := range rects {
			if seen[r] {
				t.Errorf("cell %d: two windows share the rectangle %v", cell, r)
			}
			seen[r] = true

			if i == 0 && !sameRect(r, cells[cell]) {
				t.Errorf("cell %d: the first window got %v, want the chosen cell %v", cell, r, cells[cell])
			}
		}
	}
}

// TestLayoutZonesCoversTheWholeArea checks that the zones fill the space they are
// given, with no gaps and no overlaps.
func TestLayoutZonesCoversTheWholeArea(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	rects := LayoutZones(bounds, 4, Gaps{Outer: 0, Inner: 0}, 2)

	if len(rects) != 4 {
		t.Fatalf("got %d rects, want 4", len(rects))
	}

	total := 0
	for _, r := range rects {
		if r.Width <= 0 || r.Height <= 0 {
			t.Errorf("zone %v has no area", r)
		}
		total += r.Width * r.Height
	}

	if total != bounds.Width*bounds.Height {
		t.Errorf("the zones cover %d pixels, want %d", total, bounds.Width*bounds.Height)
	}
}

// TestZoneGridShape checks the shapes people already have in their heads.
func TestZoneGridShape(t *testing.T) {
	cases := []struct {
		cells int
		cols  int
		rows  int
	}{
		{1, 1, 1},
		{2, 2, 1},
		{3, 3, 1},
		{4, 2, 2},
		{6, 3, 2},
		{9, 3, 3},
	}

	for _, tc := range cases {
		cols, rows := ZoneGridShape(tc.cells)
		if cols != tc.cols || rows != tc.rows {
			t.Errorf("a grid of %d cells is %dx%d, want %dx%d", tc.cells, cols, rows, tc.cols, tc.rows)
		}
	}
}

// TestZoneCellsHoldsTheWindows checks that the grid is big enough for the windows,
// because a picker full of empty cells is a picker full of choices that lead nowhere.
func TestZoneCellsHoldsTheWindows(t *testing.T) {
	for windows := 1; windows <= 12; windows++ {
		cells := ZoneCells(windows)
		if cells < windows {
			t.Errorf("%d windows need a grid of %d cells, got %d", windows, windows, cells)
		}
		cols, rows := ZoneGridShape(cells)
		if cols*rows < windows {
			t.Errorf("%d windows need a grid of %d cells, but %dx%d holds %d", windows, windows, cols, rows, cols*rows)
		}
	}
}

// sameRect reports whether two rectangles are the same.
func sameRect(a, b Rect) bool {
	return a.X == b.X && a.Y == b.Y && a.Width == b.Width && a.Height == b.Height
}
