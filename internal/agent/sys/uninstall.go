package sys

import "strings"

// UninstallKey is the registry key, under HKLM, of the agent's entry in
// "Settings → Apps" (Programs and Features).
const UninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\NEXMIS Agent`

// UninstallEntry describes the agent's "Settings → Apps" entry.
type UninstallEntry struct {
	DisplayName    string
	DisplayVersion string
	Publisher      string
	ExePath        string // installed nexmis-agent.exe
	SizeKB         uint32
}

// UninstallValues returns the string and DWORD values of the entry. Paths
// are Windows paths whatever the build platform (tests run on Linux too). The
// uninstall command purges everything and, being started from Settings,
// asks for elevation itself (--gui) and reports the result in a dialog.
func UninstallValues(e UninstallEntry) (strs map[string]string, dwords map[string]uint32) {
	exe := `"` + e.ExePath + `"`
	strs = map[string]string{
		"DisplayName":     e.DisplayName,
		"DisplayVersion":  e.DisplayVersion,
		"Publisher":       e.Publisher,
		"DisplayIcon":     e.ExePath + ",0",
		"InstallLocation": e.ExePath[:max(strings.LastIndexAny(e.ExePath, `\/`), 0)],
		"UninstallString": exe + " uninstall --purge --gui",
	}
	dwords = map[string]uint32{
		"NoModify":      1,
		"NoRepair":      1,
		"EstimatedSize": e.SizeKB,
	}
	return strs, dwords
}

// UninstallReport collects what an uninstall did and what it could not do.
type UninstallReport struct {
	Done     []string
	Problems []string
}

func (r *UninstallReport) Ok(msg string) { r.Done = append(r.Done, msg) }

func (r *UninstallReport) Fail(msg string, err error) {
	r.Problems = append(r.Problems, msg+"："+err.Error())
}

// String renders the report for the console or a dialog.
func (r *UninstallReport) String() string {
	var b strings.Builder
	for _, s := range r.Done {
		b.WriteString("✓ " + s + "\n")
	}
	for _, s := range r.Problems {
		b.WriteString("✗ " + s + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
