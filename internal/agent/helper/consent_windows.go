//go:build windows

package helper

import (
	"fmt"
	"sync"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

var procSetFocus = user32.NewProc("SetFocus")

const (
	idConsentAllow = 101
	idConsentDeny  = 102
	consentTimerID = 1
)

type consentState struct {
	job       *consentJob
	countdown uintptr
	skin      *winSkin
	remaining int
	done      bool
}

var (
	consentStates   = map[uintptr]*consentState{}
	consentStatesMu sync.Mutex
)

func isConsentWindow(hwnd uintptr) bool {
	consentStatesMu.Lock()
	defer consentStatesMu.Unlock()
	return consentStates[hwnd] != nil
}

func modeVerb(mode string) string {
	if mode == "control" {
		return "操作"
	}
	return "檢視"
}

// modeDescription is the consent window's plain-language description of what
// the requester will be able to do.
func modeDescription(mode string) string {
	if mode == "control" {
		return "檢視畫面並操作滑鼠與鍵盤"
	}
	return "僅檢視畫面（無法操作）"
}

func consentReason(req helperipc.ConsentRequest) string {
	if req.Reason == "" {
		return "（未提供）"
	}
	return req.Reason
}

func consentCountdownText(remaining int) string {
	return fmt.Sprintf("若未回應，將於 %d 秒後自動拒絕。", remaining)
}

// Consent window layout, in 96-DPI units.
const (
	consentW       = 460
	consentPad     = 24
	consentLabelW  = 60
	consentFooterH = 64
	consentBtnW    = 104
	consentBtnH    = 32
)

func (u *ui) openConsentWindow(job *consentJob) {
	defer recoverPanic(u.log, "openConsentWindow")

	req := job.req
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	control := req.Mode == "control"

	// Body rows, top to bottom; the footer follows the countdown.
	const rowsTop = 88
	reasonTop := rowsTop + 56
	y := reasonTop + 48
	warnTop := y
	if control {
		y += 56
	}
	countTop := y
	footerTop := countTop + 44
	clientH := int32(footerTop + consentFooterH)

	hwnd, dpi := createScaledWindow(consentClass, "NEXMIS 遠端協助請求",
		wsPopup|wsCaption|wsSysMenu, wsExTopmost, consentW, clientH)
	if hwnd == 0 {
		u.log.Error("consent window could not be created; denying", "session", req.SessionID)
		job.result <- helperipc.ConsentResult{SessionID: req.SessionID, Granted: false, Reason: "denied", User: u.user()}
		return
	}

	s := newSkin(hwnd, dpi, clrWhite)
	s.footer = clrFooter
	s.footerTop = dp(int32(footerTop), dpi)
	s.setIcon(appIconSized(dp(32, dpi)), consentPad, 22, 32)

	const textX = consentPad + 44
	s.label(hwnd, "NEXMIS 遠端協助請求", textX, 18, consentW-textX-consentPad, 28, 14, fwSemibold, clrText, clrWhite, 0)
	s.label(hwnd, "MIS 人員想要連線到您的電腦，請確認是否允許。", textX, 48, consentW-textX-consentPad, 20, 9, fwNormal, clrSubtle, clrWhite, 0)

	valueX := int32(consentPad + consentLabelW + 8)
	valueW := int32(consentW) - valueX - consentPad
	row := func(top int, label, value string, h int32, weight int32, fg uint32) {
		s.label(hwnd, label, consentPad, int32(top), consentLabelW, 22, 10, fwNormal, clrSubtle, clrWhite, 0)
		s.label(hwnd, value, valueX, int32(top), valueW, h, 10, weight, fg, clrWhite, 0)
	}
	row(rowsTop, "請求者", req.Requester, 22, fwSemibold, clrText)
	modeFg := uint32(clrText)
	if control {
		modeFg = clrWarn
	}
	row(rowsTop+28, "模式", modeDescription(req.Mode), 22, fwSemibold, modeFg)
	row(reasonTop, "原因", consentReason(req), 44, fwNormal, clrText)

	if control {
		s.label(hwnd, "", consentPad, int32(warnTop), consentW-2*consentPad, 44, 9, fwNormal, clrWarn, clrWarnBg, 0)
		s.label(hwnd, "注意：允許後對方可以操作您的滑鼠與鍵盤。\n若您不清楚這項請求，請按「拒絕」。",
			consentPad+12, int32(warnTop)+5, consentW-2*consentPad-24, 36, 9, fwNormal, clrWarn, clrWarnBg, 0)
	}

	st := &consentState{job: job, remaining: timeout}
	st.countdown = s.label(hwnd, consentCountdownText(timeout), consentPad, int32(countTop), consentW-2*consentPad, 20, 9, fwNormal, clrSubtle, clrWhite, 0)
	barColour := uint32(clrAccent)
	if control {
		barColour = clrWarn
	}
	s.setBar(consentPad, int32(countTop)+26, consentW-2*consentPad, 4, timeout, barColour)
	st.skin = s

	btnY := int32(footerTop + (consentFooterH-consentBtnH)/2)
	denyX := int32(consentW - consentPad - consentBtnW)
	allowX := denyX - 8 - consentBtnW
	s.button(hwnd, "允許", allowX, btnY, consentBtnW, consentBtnH, idConsentAllow, true, clrAccent, 0)
	deny := s.button(hwnd, "拒絕", denyX, btnY, consentBtnW, consentBtnH, idConsentDeny, false, 0, bsDefPushButt)

	consentStatesMu.Lock()
	consentStates[hwnd] = st
	consentStatesMu.Unlock()

	procShowWindow.Call(hwnd, swShow)
	procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWindow)
	procSetForegroundWindow.Call(hwnd)
	procSetFocus.Call(deny) // default focus on the safe choice
	procSetTimer.Call(hwnd, consentTimerID, 1000, 0)
	u.log.Info("consent window shown", "session", req.SessionID, "mode", req.Mode,
		"requester", req.Requester, "timeout_sec", timeout, "dpi", dpi)
}

func consentFinish(hwnd uintptr, granted bool, reason string) {
	consentStatesMu.Lock()
	st := consentStates[hwnd]
	if st == nil || st.done {
		consentStatesMu.Unlock()
		return
	}
	st.done = true
	delete(consentStates, hwnd)
	consentStatesMu.Unlock()

	procKillTimer.Call(hwnd, consentTimerID)
	user := ""
	if theUI != nil {
		user = theUI.user()
		theUI.log.Info("consent answered", "session", st.job.req.SessionID,
			"granted", granted, "reason", reason, "user", user)
	}
	st.job.result <- helperipc.ConsentResult{
		SessionID: st.job.req.SessionID,
		Granted:   granted,
		Reason:    reason,
		User:      user,
	}
	procDestroyWindow.Call(hwnd)
}

func consentWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	if r, ok := skinProc(hwnd, m, wp, lp); ok {
		return r
	}
	switch m {
	case wmTimer:
		consentStatesMu.Lock()
		st := consentStates[hwnd]
		consentStatesMu.Unlock()
		if st == nil {
			return 0
		}
		st.remaining--
		if st.remaining <= 0 {
			consentFinish(hwnd, false, "timeout")
			return 0
		}
		setText(st.countdown, consentCountdownText(st.remaining))
		st.skin.updateBar(hwnd, st.remaining)
		return 0
	case wmCommand:
		switch uint16(loword(wp)) {
		case idConsentAllow:
			consentFinish(hwnd, true, "")
		case idConsentDeny:
			consentFinish(hwnd, false, "denied")
		}
		return 0
	case wmClose:
		consentFinish(hwnd, false, "denied")
		return 0
	case wmDestroy:
		consentFinish(hwnd, false, "denied")
		dropSkin(hwnd)
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
