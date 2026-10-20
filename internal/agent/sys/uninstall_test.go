package sys

import (
	"errors"
	"testing"
)

func TestUninstallValues(t *testing.T) {
	strs, dwords := UninstallValues(UninstallEntry{
		DisplayName:    "NEXMIS Agent",
		DisplayVersion: "1.0.1",
		Publisher:      "NEXMIS",
		ExePath:        `C:\Program Files\NEXMIS\Agent\nexmis-agent.exe`,
		SizeKB:         12345,
	})
	want := map[string]string{
		"DisplayName":     "NEXMIS Agent",
		"DisplayVersion":  "1.0.1",
		"Publisher":       "NEXMIS",
		"DisplayIcon":     `C:\Program Files\NEXMIS\Agent\nexmis-agent.exe,0`,
		"UninstallString": `"C:\Program Files\NEXMIS\Agent\nexmis-agent.exe" uninstall --purge --gui`,
	}
	for k, v := range want {
		if strs[k] != v {
			t.Errorf("%s = %q, want %q", k, strs[k], v)
		}
	}
	if strs["InstallLocation"] != `C:\Program Files\NEXMIS\Agent` {
		t.Errorf("InstallLocation = %q", strs["InstallLocation"])
	}
	if dwords["NoModify"] != 1 || dwords["NoRepair"] != 1 || dwords["EstimatedSize"] != 12345 {
		t.Errorf("dwords = %v", dwords)
	}
}

func TestUninstallReport(t *testing.T) {
	var r UninstallReport
	r.Ok("已刪除服務")
	r.Fail("刪除資料夾", errors.New("拒絕存取"))
	if got := r.String(); got != "✓ 已刪除服務\n✗ 刪除資料夾：拒絕存取" {
		t.Errorf("report = %q", got)
	}
}
