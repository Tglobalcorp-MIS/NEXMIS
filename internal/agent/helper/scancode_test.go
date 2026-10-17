package helper

import "testing"

func TestLookupScanCodeBasics(t *testing.T) {
	cases := []struct {
		code string
		sc   uint16
		ext  bool
	}{
		{"KeyA", 0x1E, false},
		{"KeyZ", 0x2C, false},
		{"Digit1", 0x02, false},
		{"Digit0", 0x0B, false},
		{"Enter", 0x1C, false},
		{"Escape", 0x01, false},
		{"F12", 0x58, false},
		{"ArrowUp", 0x48, true},
		{"ArrowRight", 0x4D, true},
		{"ControlRight", 0x1D, true},
		{"ControlLeft", 0x1D, false},
		{"NumpadDivide", 0x35, true},
		{"NumpadEnter", 0x1C, true},
		{"MetaLeft", 0x5B, true},
	}
	for _, c := range cases {
		sc, ext, ok := lookupScanCode(c.code)
		if !ok {
			t.Fatalf("%s not found", c.code)
		}
		if sc != c.sc || ext != c.ext {
			t.Fatalf("%s = (%#x, %v), want (%#x, %v)", c.code, sc, ext, c.sc, c.ext)
		}
	}
}

func TestLookupScanCodeUnknown(t *testing.T) {
	if _, _, ok := lookupScanCode("Nonexistent"); ok {
		t.Fatal("unknown code should not be found")
	}
}

func TestScanCodeTableComplete(t *testing.T) {
	// All 26 letters and 10 digits must be present.
	for c := 'A'; c <= 'Z'; c++ {
		if _, _, ok := lookupScanCode("Key" + string(c)); !ok {
			t.Fatalf("missing Key%c", c)
		}
	}
	for d := 0; d <= 9; d++ {
		if _, _, ok := lookupScanCode("Digit" + string(rune('0'+d))); !ok {
			t.Fatalf("missing Digit%d", d)
		}
	}
}
