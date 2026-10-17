package helper

import (
	"image"
	"testing"
)

func newTestFrame(w, h int) *frameBuf {
	return &frameBuf{pix: make([]byte, w*h*4), stride: w * 4, width: w, height: h}
}

func TestChangedTilesFullWhenNoPrev(t *testing.T) {
	cur := newTestFrame(128, 128)
	got := changedTiles(nil, cur, 64)
	if len(got) != 4 {
		t.Fatalf("expected 4 tiles for 128x128/64, got %d", len(got))
	}
}

func TestChangedTilesDetectsChange(t *testing.T) {
	prev := newTestFrame(128, 64)
	cur := newTestFrame(128, 64)
	// Change one pixel in the top-right tile (x=64..128).
	off := 0*cur.stride + 70*4
	cur.pix[off] = 0xFF
	got := changedTiles(prev, cur, 64)
	if len(got) != 1 {
		t.Fatalf("expected 1 changed tile, got %d: %v", len(got), got)
	}
	want := image.Rect(64, 0, 128, 64)
	if got[0] != want {
		t.Fatalf("changed rect = %v, want %v", got[0], want)
	}
}

func TestChangedTilesNoChange(t *testing.T) {
	prev := newTestFrame(64, 64)
	cur := newTestFrame(64, 64)
	if got := changedTiles(prev, cur, 64); len(got) != 0 {
		t.Fatalf("expected no changes, got %v", got)
	}
}

func TestChangedTilesPartialEdge(t *testing.T) {
	// 100x100 with tile 64 -> tiles at x {0,64}, y {0,64}; edge tiles are 36 wide/high.
	prev := newTestFrame(100, 100)
	cur := newTestFrame(100, 100)
	cur.pix[0] = 1 // top-left tile
	got := changedTiles(prev, cur, 64)
	if len(got) != 1 || got[0] != image.Rect(0, 0, 64, 64) {
		t.Fatalf("got %v", got)
	}
}

func TestMergeRectsHorizontal(t *testing.T) {
	rects := []image.Rectangle{
		image.Rect(0, 0, 64, 64),
		image.Rect(64, 0, 128, 64),
		image.Rect(128, 0, 192, 64),
	}
	got := mergeRects(rects, 64)
	if len(got) != 1 || got[0] != image.Rect(0, 0, 192, 64) {
		t.Fatalf("horizontal merge = %v", got)
	}
}

func TestMergeRectsVertical(t *testing.T) {
	rects := []image.Rectangle{
		image.Rect(0, 0, 64, 64),
		image.Rect(0, 64, 64, 128),
	}
	got := mergeRects(rects, 64)
	if len(got) != 1 || got[0] != image.Rect(0, 0, 64, 128) {
		t.Fatalf("vertical merge = %v", got)
	}
}

func TestMergeRectsBlock(t *testing.T) {
	// A 2x2 block of tiles should merge into one 128x128 rectangle.
	rects := []image.Rectangle{
		image.Rect(0, 0, 64, 64), image.Rect(64, 0, 128, 64),
		image.Rect(0, 64, 64, 128), image.Rect(64, 64, 128, 128),
	}
	got := mergeRects(rects, 64)
	if len(got) != 1 || got[0] != image.Rect(0, 0, 128, 128) {
		t.Fatalf("block merge = %v", got)
	}
}

func TestMergeRectsDisjoint(t *testing.T) {
	rects := []image.Rectangle{
		image.Rect(0, 0, 64, 64),
		image.Rect(192, 192, 256, 256),
	}
	got := mergeRects(rects, 64)
	if len(got) != 2 {
		t.Fatalf("disjoint should stay 2, got %d: %v", len(got), got)
	}
}

// coverage sums the areas; merged output must cover the same total area as input
// for a non-overlapping input set.
func TestMergeRectsPreservesArea(t *testing.T) {
	in := []image.Rectangle{
		image.Rect(0, 0, 64, 64), image.Rect(64, 0, 128, 64),
		image.Rect(0, 64, 64, 128),
	}
	area := func(rs []image.Rectangle) int {
		a := 0
		for _, r := range rs {
			a += r.Dx() * r.Dy()
		}
		return a
	}
	if area(mergeRects(in, 64)) != area(in) {
		t.Fatalf("area not preserved")
	}
}
