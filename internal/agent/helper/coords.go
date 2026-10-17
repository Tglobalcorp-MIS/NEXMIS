package helper

import "image"

// toVirtualAbsolute converts a pixel position within a selected monitor into the
// 0..65535 normalised absolute coordinate SendInput expects with
// MOUSEEVENTF_ABSOLUTE|MOUSEEVENTF_VIRTUALDESK. x and y are pixels relative to
// the monitor's top-left; mon is the monitor rectangle in virtual-desktop pixel
// coordinates; virt is the whole virtual desktop rectangle.
func toVirtualAbsolute(x, y int, mon, virt image.Rectangle) (int32, int32) {
	absX := mon.Min.X + x
	absY := mon.Min.Y + y
	return normalize(absX, virt.Min.X, virt.Dx()), normalize(absY, virt.Min.Y, virt.Dy())
}

// normalize maps an absolute coordinate within [origin, origin+span) to
// [0, 65535], clamping to the valid range.
func normalize(value, origin, span int) int32 {
	if span <= 1 {
		return 0
	}
	rel := value - origin
	if rel < 0 {
		rel = 0
	}
	if rel > span-1 {
		rel = span - 1
	}
	// Round to nearest to reduce drift.
	return int32((rel*65535 + (span-1)/2) / (span - 1))
}
