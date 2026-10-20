package core

import "testing"

func TestReleaseFingerprint(t *testing.T) {
	m := AgentManifest{Version: "1.0.1", Builds: map[string]AgentManifestFile{
		"windows/amd64": {File: "a.exe", SHA256: "AA"},
		"windows/arm64": {File: "b.exe", SHA256: "bb"},
	}}
	fp := releaseFingerprint(m, true)
	if fp != "1.0.1;windows/amd64=aa,windows/arm64=bb" {
		t.Fatalf("fingerprint = %q", fp)
	}
	if releaseFingerprint(m, false) != "off" {
		t.Fatal("auto-update off must give the off fingerprint")
	}
	if releaseFingerprint(AgentManifest{Version: "1.0.1"}, true) != "off" {
		t.Fatal("a release without builds is not published")
	}
	m2 := m
	m2.Builds = map[string]AgentManifestFile{"windows/amd64": {SHA256: "cc"}, "windows/arm64": {SHA256: "bb"}}
	if releaseFingerprint(m2, true) == fp {
		t.Fatal("a rebuilt binary with the same version must change the fingerprint")
	}
}

func TestVersionState(t *testing.T) {
	cases := map[[2]string]string{
		{"1.0.0", "1.0.1"}: "outdated",
		{"1.0.1", "1.0.1"}: "current",
		{"1.1.0", "1.0.1"}: "newer",
		{"dev", "1.0.1"}:   "unknown",
		{"", "1.0.1"}:      "unknown",
		{"1.0.0", ""}:      "unknown",
		{"dev", "dev"}:     "current",
	}
	for in, want := range cases {
		if got := versionState(in[0], in[1]); got != want {
			t.Errorf("versionState(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
