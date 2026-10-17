package store

import "testing"

func TestSoftwareChanges(t *testing.T) {
	old := map[swKey][]string{
		{"7-Zip", "x64", "machine"}:       {"23.01"},
		{"7-Zip", "x86", "machine"}:       {"23.01"},
		{"Zoom", "x64", "user"}:           {"5.0"},
		{"VC++ Runtime", "x64", ""}:       {"14.30", "14.36"},
		{"Removed App", "x64", "machine"}: {"1.0"},
	}
	cur := map[swKey][]string{
		{"7-Zip", "x64", "machine"}:   {"24.08"},
		{"7-Zip", "x86", "machine"}:   {"23.01"},
		{"Zoom", "x64", "user"}:       {"5.0"},
		{"VC++ Runtime", "x64", ""}:   {"14.36", "14.40"},
		{"New App", "x64", "machine"}: {"2.0"},
	}
	got := softwareChanges(old, cur)
	want := []InventoryChange{
		{Change: "changed", Item: "7-Zip (x64)", Old: "23.01", New: "24.08"},
		{Change: "added", Item: "New App (x64)", New: "2.0"},
		{Change: "removed", Item: "Removed App (x64)", Old: "1.0"},
		{Change: "added", Item: "VC++ Runtime (x64)", New: "14.40"},
		{Change: "removed", Item: "VC++ Runtime (x64)", Old: "14.30"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}
