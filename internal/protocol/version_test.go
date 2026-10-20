package protocol

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.2.10", "1.2.9", true},
		{"2.0.0", "1.99.99", true},
		{"v1.1", "1.0.9", true},
		{"1.1.0", "v1.0.0", true},
		{"1.0.1-rc1", "1.0.0", true},
		{"1.0.0+build5", "0.9.0", true},
		{" 1.0.1 ", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.2.9", "1.2.10", false},
		{"1.0.0-rc2", "1.0.0", false}, // suffixes are ignored: same version
		{"dev", "1.0.0", false},
		{"1.0.1", "dev", false},
		{"", "1.0.0", false},
		{"1.0.1", "", false},
		{"1.0.0.1", "1.0.0", false},
		{"1.x.0", "1.0.0", false},
		{"1.-1.0", "1.0.0", false},
	}
	for _, c := range cases {
		if got := NewerVersion(c.candidate, c.current); got != c.want {
			t.Errorf("NewerVersion(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, ok := ParseVersion("v3.4.5-beta"); !ok || v != [3]int{3, 4, 5} {
		t.Fatalf("ParseVersion = %v %v", v, ok)
	}
	if _, ok := ParseVersion("dev"); ok {
		t.Fatal("dev must not parse")
	}
}
