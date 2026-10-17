package ad

import (
	"testing"
	"time"
)

func TestNormalizeUsername(t *testing.T) {
	for in, want := range map[string]string{
		`CORP\alice`:       "alice",
		"alice@corp.local": "alice",
		" alice ":          "alice",
	} {
		if got := NormalizeUsername(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestFileTime(t *testing.T) {
	// 2024-01-01T00:00:00Z
	got := FileTime("133485408000000000")
	if got == nil || !got.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v", got)
	}
	if FileTime("0") != nil || FileTime("x") != nil {
		t.Fatal("expected nil")
	}
}

func TestRoleFor(t *testing.T) {
	rank := func(r string) int { return map[string]int{"admin": 4, "operator": 3, "viewer": 1}[r] }
	s := Settings{RoleMappings: []RoleMapping{
		{GroupDN: "CN=MIS-View,DC=corp", Role: "viewer"},
		{GroupDN: "CN=MIS-Ops,DC=corp", Role: "operator"},
	}}
	if r := s.RoleFor([]string{"cn=mis-view,dc=corp", "CN=MIS-Ops,DC=corp"}, rank); r != "operator" {
		t.Fatalf("got %q", r)
	}
	if r := s.RoleFor([]string{"CN=Other"}, rank); r != "" {
		t.Fatalf("got %q", r)
	}
}
