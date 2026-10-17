package helper

import (
	"image"
	"sort"
)

// frameBuf holds a captured frame as packed BGRA pixels (4 bytes/pixel), the
// layout produced by GetDIBits on Windows.
type frameBuf struct {
	pix    []byte
	stride int // bytes per row (>= 4*width)
	width  int
	height int
}

// equalSize reports whether two frames share dimensions.
func (f *frameBuf) equalSize(o *frameBuf) bool {
	return o != nil && f.width == o.width && f.height == o.height && f.stride == o.stride
}

// changedTiles returns the tile-aligned rectangles that differ between prev and
// cur. If prev is nil or sized differently, every tile is returned (full frame).
func changedTiles(prev, cur *frameBuf, tile int) []image.Rectangle {
	if tile <= 0 {
		tile = 64
	}
	var out []image.Rectangle
	full := !cur.equalSize(prev)
	for y := 0; y < cur.height; y += tile {
		th := tile
		if y+th > cur.height {
			th = cur.height - y
		}
		for x := 0; x < cur.width; x += tile {
			tw := tile
			if x+tw > cur.width {
				tw = cur.width - x
			}
			if full || tileDiffers(prev, cur, x, y, tw, th) {
				out = append(out, image.Rect(x, y, x+tw, y+th))
			}
		}
	}
	return out
}

// tileDiffers compares one tile region byte-for-byte.
func tileDiffers(prev, cur *frameBuf, x, y, w, h int) bool {
	bpp := 4
	for row := 0; row < h; row++ {
		co := (y+row)*cur.stride + x*bpp
		po := (y+row)*prev.stride + x*bpp
		n := w * bpp
		if co+n > len(cur.pix) || po+n > len(prev.pix) {
			return true
		}
		if !bytesEqual(cur.pix[co:co+n], prev.pix[po:po+n]) {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mergeRects coalesces tile-aligned rectangles (each a multiple of tile, origin
// on a tile boundary) into fewer, larger rectangles covering exactly the same
// area, to reduce the number of blocks sent per frame.
func mergeRects(rects []image.Rectangle, tile int) []image.Rectangle {
	if tile <= 0 || len(rects) <= 1 {
		return rects
	}
	// Map tile-grid cells that are present.
	type cell struct{ cx, cy int }
	present := make(map[cell]image.Rectangle, len(rects))
	for _, r := range rects {
		present[cell{r.Min.X / tile, r.Min.Y / tile}] = r
	}

	// Horizontal pass: merge consecutive cells in each row into strips.
	type strip struct {
		rect image.Rectangle
		cy   int
	}
	// Walk cells left-to-right within each row so every strip starts at its
	// leftmost cell (map iteration order is random).
	cells := make([]cell, 0, len(present))
	for c := range present {
		cells = append(cells, c)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].cy != cells[j].cy {
			return cells[i].cy < cells[j].cy
		}
		return cells[i].cx < cells[j].cx
	})
	used := make(map[cell]bool, len(rects))
	var strips []strip
	for _, c := range cells {
		r := present[c]
		if used[c] {
			continue
		}
		cx := c.cx
		merged := r
		used[c] = true
		for {
			nc := cell{cx + 1, c.cy}
			nr, ok := present[nc]
			if !ok || used[nc] {
				break
			}
			merged = merged.Union(nr)
			used[nc] = true
			cx++
		}
		strips = append(strips, strip{rect: merged, cy: c.cy})
	}

	// Process strips top-to-bottom so a merge always starts from the topmost
	// row of a run (map iteration order is otherwise random).
	sort.Slice(strips, func(i, j int) bool {
		if strips[i].cy != strips[j].cy {
			return strips[i].cy < strips[j].cy
		}
		return strips[i].rect.Min.X < strips[j].rect.Min.X
	})

	// Vertical pass: merge strips in adjacent rows that share x extent.
	usedStrip := make([]bool, len(strips))
	byRow := make(map[int][]int) // cy -> indices
	for i, s := range strips {
		byRow[s.cy] = append(byRow[s.cy], i)
	}
	var out []image.Rectangle
	for i, s := range strips {
		if usedStrip[i] {
			continue
		}
		merged := s.rect
		usedStrip[i] = true
		cy := s.cy
		for {
			var next int = -1
			for _, j := range byRow[cy+1] {
				if usedStrip[j] {
					continue
				}
				if strips[j].rect.Min.X == merged.Min.X && strips[j].rect.Max.X == merged.Max.X {
					next = j
					break
				}
			}
			if next < 0 {
				break
			}
			merged = merged.Union(strips[next].rect)
			usedStrip[next] = true
			cy++
		}
		out = append(out, merged)
	}
	return out
}
