package collect

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Health rule thresholds. Keep every tunable here so the rules stay easy to
// audit and adjust.
const (
	DiskFreeWarnPct     = 10.0 // free space below this % -> warning
	DiskFreeCritPct     = 5.0  // free space below this % -> critical
	MemHighPct          = 90.0
	CPUHighPct          = 90.0
	RebootPendingWarn   = 7 * 24 * time.Hour  // pending reboot + uptime above this -> warning
	UptimeLongWarn      = 30 * 24 * time.Hour // uptime above this -> warning
	DefenderSigMaxDays  = 7
	UpdatesStaleAfter   = 45 * 24 * time.Hour
	EventErrorsWarn     = 50 // critical+error events in 24h above this -> warning
	BatteryHealthWarn   = 70.0
	BatteryHealthCrit   = 50.0
	BatteryCyclesNotice = 800
)

// Issue codes.
const (
	IssueDiskLow             = "disk_low"
	IssueDiskUnhealthy       = "disk_unhealthy"
	IssueMemHigh             = "mem_high"
	IssueCPUHigh             = "cpu_high"
	IssuePendingReboot       = "pending_reboot"
	IssueUptimeLong          = "uptime_long"
	IssueDefenderOff         = "defender_off"
	IssueDefenderRealtimeOff = "defender_realtime_off"
	IssueDefenderSigOld      = "defender_sig_old"
	IssueFirewallOff         = "firewall_off"
	IssueUpdatesStale        = "updates_stale"
	IssueEventErrors         = "event_errors"
	IssueBatteryDegraded     = "battery_degraded"
	IssueBatteryCycles       = "battery_cycles"
)

const gib = 1 << 30

// EvaluateHealth derives Issues from a health snapshot and (optionally) a
// battery snapshot. h.Collected is used as "now" so results are
// reproducible; when it is zero the current time is used. The result is
// ordered critical -> warning -> info, rules keeping their relative order.
func EvaluateHealth(h *protocol.Health, b *protocol.Battery) []protocol.Issue {
	var out []protocol.Issue
	add := func(code, sev, format string, args ...any) {
		out = append(out, protocol.Issue{Code: code, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}

	if h != nil {
		now := h.Collected
		if now.IsZero() {
			now = time.Now()
		}
		uptime := time.Duration(h.UptimeSec) * time.Second

		for _, v := range h.Volumes {
			if v.Size == 0 {
				continue
			}
			pct := float64(v.Free) / float64(v.Size) * 100
			sev := ""
			switch {
			case pct < DiskFreeCritPct:
				sev = protocol.SevCritical
			case pct < DiskFreeWarnPct:
				sev = protocol.SevWarning
			}
			if sev != "" {
				add(IssueDiskLow, sev, "%s 剩餘 %.1f%%（%.1f GB）", v.Mount, pct, float64(v.Free)/gib)
			}
		}

		for _, d := range h.DiskHealth {
			name := d.Model
			if name == "" {
				name = d.SerialNumber
			}
			if name == "" {
				name = "未知磁碟"
			}
			switch d.Health {
			case "Unhealthy":
				add(IssueDiskUnhealthy, protocol.SevCritical, "磁碟 %s 健康狀態異常（Unhealthy），請儘速備份並更換", name)
			case "Warning":
				add(IssueDiskUnhealthy, protocol.SevWarning, "磁碟 %s 健康狀態警告（Warning），建議備份並檢查", name)
			}
		}

		if h.MemPercent > MemHighPct {
			add(IssueMemHigh, protocol.SevWarning, "記憶體使用率 %.1f%%，超過 %.0f%%", h.MemPercent, MemHighPct)
		}
		if h.CPUPercent > CPUHighPct {
			add(IssueCPUHigh, protocol.SevWarning, "CPU 使用率 %.1f%%，超過 %.0f%%", h.CPUPercent, CPUHighPct)
		}

		if h.PendingReboot {
			why := ""
			if len(h.PendingRebootWhy) > 0 {
				why = "（" + strings.Join(h.PendingRebootWhy, "、") + "）"
			}
			if uptime > RebootPendingWarn {
				add(IssuePendingReboot, protocol.SevWarning, "系統等待重新開機%s，且已 %d 天未重新開機", why, int(uptime.Hours()/24))
			} else {
				add(IssuePendingReboot, protocol.SevInfo, "系統等待重新開機%s", why)
			}
		}
		if uptime > UptimeLongWarn {
			add(IssueUptimeLong, protocol.SevWarning, "已連續運作 %d 天未重新開機", int(uptime.Hours()/24))
		}

		defenderOn := h.DefenderEnabled == nil || *h.DefenderEnabled
		if h.DefenderEnabled != nil && !*h.DefenderEnabled {
			add(IssueDefenderOff, protocol.SevCritical, "Microsoft Defender 防毒已停用")
		}
		if defenderOn && h.DefenderRealtime != nil && !*h.DefenderRealtime {
			add(IssueDefenderRealtimeOff, protocol.SevCritical, "Microsoft Defender 即時保護已關閉")
		}
		if defenderOn && h.DefenderSigAgeDay != nil && *h.DefenderSigAgeDay > DefenderSigMaxDays {
			add(IssueDefenderSigOld, protocol.SevWarning, "Microsoft Defender 病毒碼已 %d 天未更新", *h.DefenderSigAgeDay)
		}

		if h.FirewallEnabled != nil && !*h.FirewallEnabled {
			add(IssueFirewallOff, protocol.SevWarning, "Windows 防火牆未啟用（至少一個設定檔已關閉）")
		}

		if h.LastUpdateInstall != nil && !h.LastUpdateInstall.IsZero() {
			age := now.Sub(*h.LastUpdateInstall)
			if age > UpdatesStaleAfter {
				add(IssueUpdatesStale, protocol.SevWarning, "已 %d 天未安裝 Windows 更新（最後安裝：%s）",
					int(age.Hours()/24), h.LastUpdateInstall.Local().Format("2006-01-02"))
			}
		}

		if h.EventErrors24h > EventErrorsWarn {
			add(IssueEventErrors, protocol.SevWarning, "過去 24 小時有 %d 筆嚴重/錯誤事件記錄", h.EventErrors24h)
		}
	}

	if b != nil && b.Present {
		if b.HealthPercent > 0 {
			switch {
			case b.HealthPercent < BatteryHealthCrit:
				add(IssueBatteryDegraded, protocol.SevCritical, "電池健康度 %.1f%%，已嚴重老化，建議更換", b.HealthPercent)
			case b.HealthPercent < BatteryHealthWarn:
				add(IssueBatteryDegraded, protocol.SevWarning, "電池健康度 %.1f%%，電池已老化", b.HealthPercent)
			}
		}
		if b.CycleCount > BatteryCyclesNotice {
			add(IssueBatteryCycles, protocol.SevInfo, "電池循環次數 %d 次，已超過 %d 次", b.CycleCount, BatteryCyclesNotice)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return sevRank(out[i].Severity) > sevRank(out[j].Severity) })
	return out
}

func sevRank(s string) int {
	switch s {
	case protocol.SevCritical:
		return 2
	case protocol.SevWarning:
		return 1
	}
	return 0
}
