package helper

import (
	"image"
	"testing"
)

func TestNormalizeEndpoints(t *testing.T) {
	if n := normalize(0, 0, 1920); n != 0 {
		t.Fatalf("left edge = %d, want 0", n)
	}
	if n := normalize(1919, 0, 1920); n != 65535 {
		t.Fatalf("right edge = %d, want 65535", n)
	}
	if n := normalize(960, 0, 1920); n < 32000 || n > 33000 {
		t.Fatalf("mid = %d, expected ~32768", n)
	}
}

func TestNormalizeClamp(t *testing.T) {
	if n := normalize(-50, 0, 1920); n != 0 {
		t.Fatalf("below range = %d, want 0", n)
	}
	if n := normalize(5000, 0, 1920); n != 65535 {
		t.Fatalf("above range = %d, want 65535", n)
	}
}

func TestToVirtualAbsolutePrimaryMonitor(t *testing.T) {
	// Single 1920x1080 desktop; monitor == virtual desktop.
	mon := image.Rect(0, 0, 1920, 1080)
	virt := mon
	ax, ay := toVirtualAbsolute(0, 0, mon, virt)
	if ax != 0 || ay != 0 {
		t.Fatalf("origin = (%d,%d)", ax, ay)
	}
	ax, ay = toVirtualAbsolute(1919, 1079, mon, virt)
	if ax != 65535 || ay != 65535 {
		t.Fatalf("far corner = (%d,%d)", ax, ay)
	}
}

func TestToVirtualAbsoluteSecondMonitor(t *testing.T) {
	// Two side-by-side 1920-wide monitors; select the right one.
	virt := image.Rect(0, 0, 3840, 1080)
	mon := image.Rect(1920, 0, 3840, 1080)
	// Top-left pixel of the right monitor maps halfway across the desktop.
	ax, _ := toVirtualAbsolute(0, 0, mon, virt)
	want := normalize(1920, 0, 3840)
	if ax != want {
		t.Fatalf("second monitor origin x = %d, want %d", ax, want)
	}
	// Far-right pixel maps to the extreme right.
	ax, _ = toVirtualAbsolute(1919, 0, mon, virt)
	if ax != 65535 {
		t.Fatalf("second monitor right edge = %d, want 65535", ax)
	}
}

func TestToVirtualAbsoluteNegativeOrigin(t *testing.T) {
	// A left monitor positioned at negative virtual coordinates.
	virt := image.Rect(-1920, 0, 1920, 1080)
	mon := image.Rect(-1920, 0, 0, 1080)
	ax, _ := toVirtualAbsolute(0, 0, mon, virt)
	if ax != 0 {
		t.Fatalf("negative-origin monitor left = %d, want 0", ax)
	}
}
