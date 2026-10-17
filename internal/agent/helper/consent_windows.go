//go:build windows

package helper

import (
	"fmt"
	"sync"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

var procSetFocus = user32.NewProc("SetFocus")

const (
	idConsentAllow = 1
	idConsentDeny  = 2
	consentTimerID = 1
)

type consentState struct {
	job       *consentJob
	static    uintptr
	remaining int
	done      bool
}

var (
	consentStates   = map[uintptr]*consentState{}
	consentStatesMu sync.Mutex
)

func modeVerb(mode string) string {
	if mode == "control" {
		return "操作"
	}
	return "檢視"
}

func (u *ui) openConsentWindow(job *consentJob) {
	defer recoverPanic(u.log, "openConsentWindow")

	timeout := job.req.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	const w, h = 440, 230
	hwnd := createTopWindow(consentClass, "遠端連線請求",
		wsPopup|wsCaption|wsSysMenu, wsExTopmost, w, h)
	if hwnd == 0 {
		job.result <- helperipc.ConsentResult{SessionID: job.req.SessionID, Granted: false, Reason: "denied", User: u.user()}
		return
	}

	st := &consentState{job: job, remaining: timeout}
	st.static = createControl(hwnd, staticClassName, consentText(job.req, timeout),
		ssLeft|uint32(wsChild)|wsVisible, 20, 20, w-40, h-100, 0)
	createControl(hwnd, buttonClassName, "允許",
		uint32(wsChild)|wsVisible|wsTabStop|bsPushButton, 70, h-70, 120, 32, idConsentAllow)
	deny := createControl(hwnd, buttonClassName, "拒絕",
		uint32(wsChild)|wsVisible|wsTabStop|bsDefPushButt, 250, h-70, 120, 32, idConsentDeny)

	consentStatesMu.Lock()
	consentStates[hwnd] = st
	consentStatesMu.Unlock()

	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	procSetFocus.Call(deny) // default focus on the safe choice
	procSetTimer.Call(hwnd, consentTimerID, 1000, 0)
}

func consentText(req helperipc.ConsentRequest, remaining int) string {
	reason := req.Reason
	if reason == "" {
		reason = "（未提供）"
	}
	return fmt.Sprintf("%s 請求%s您的電腦畫面。\n\n原因：%s\n\n將於 %d 秒後自動拒絕。",
		req.Requester, modeVerb(req.Mode), reason, remaining)
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
		setText(st.static, consentText(st.job.req, st.remaining))
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
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
