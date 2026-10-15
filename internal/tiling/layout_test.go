package tiling

import (
	"testing"
)

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
	// Total area check
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
