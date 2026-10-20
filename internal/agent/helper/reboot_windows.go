//go:build windows

package helper

import (
	"fmt"
	"sync"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

const (
	idRebootNow   = 101
	idRebootDefer = 102
	rebootTimerID = 2

	comboClassName = "COMBOBOX"
	cbAddString    = 0x0143
	cbSetCurSel    = 0x014E
	cbGetCurSel    = 0x0147
	cbsDropdownLst = 0x0003
)

var deferOptions = []int{15, 30, 60}

type rebootState struct {
	job       *rebootJob
	countdown uintptr
	skin      *winSkin
	combo     uintptr
	remaining int
	shutdown  bool
	done      bool
}

var (
	rebootStates   = map[uintptr]*rebootState{}
	rebootStatesMu sync.Mutex
)

func isRebootWindow(hwnd uintptr) bool {
	rebootStatesMu.Lock()
	defer rebootStatesMu.Unlock()
	return rebootStates[hwnd] != nil
}

// Reboot window layout, in 96-DPI units (shares the consent window's frame).
const rebootFooterH = 64

func (u *ui) openRebootWindow(job *rebootJob) {
	defer recoverPanic(u.log, "openRebootWindow")

	remaining := job.req.CountdownSec
	if remaining <= 0 {
		remaining = 300
	}
	y := 88
	msgTop := y
	if job.req.Message != "" {
		y += 52
	}
	countTop := y
	footerTop := countTop + 52
	clientH := int32(footerTop + rebootFooterH)

	hwnd, dpi := createScaledWindow(rebootClass, rebootTitle(job.req.Shutdown),
		wsPopup|wsCaption|wsSysMenu, wsExTopmost, consentW, clientH)
	if hwnd == 0 {
		job.result <- helperipc.RebootResponse{ID: job.req.ID, Action: helperipc.RebootNow}
		return
	}

	s := newSkin(hwnd, dpi, clrWhite)
	s.footer = clrFooter
	s.footerTop = dp(int32(footerTop), dpi)
	s.setIcon(appIconSized(dp(32, dpi)), consentPad, 22, 32)

	const textX = consentPad + 44
	s.label(hwnd, rebootTitle(job.req.Shutdown), textX, 18, consentW-textX-consentPad, 28, 14, fwSemibold, clrText, clrWhite, 0)
	s.label(hwnd, "MIS 已排定此作業，請先儲存並關閉您正在編輯的檔案。", textX, 48, consentW-textX-consentPad, 20, 9, fwNormal, clrSubtle, clrWhite, 0)
	if job.req.Message != "" {
		s.label(hwnd, job.req.Message, consentPad, int32(msgTop), consentW-2*consentPad, 44, 10, fwNormal, clrText, clrWhite, 0)
	}

	st := &rebootState{job: job, remaining: remaining, shutdown: job.req.Shutdown}
	st.countdown = s.label(hwnd, rebootText(job.req, remaining), consentPad, int32(countTop), consentW-2*consentPad, 24, 11, fwSemibold, clrText, clrWhite, 0)
	s.setBar(consentPad, int32(countTop)+32, consentW-2*consentPad, 4, remaining, clrAccent)
	st.skin = s

	nowLabel := "立即重新啟動"
	if job.req.Shutdown {
		nowLabel = "立即關機"
	}
	btnY := int32(footerTop + (rebootFooterH-consentBtnH)/2)
	nowW := int32(128)
	s.button(hwnd, nowLabel, consentW-consentPad-nowW, btnY, nowW, consentBtnH, idRebootNow, true, clrAccent, 0)

	var deferBtn uintptr
	if job.req.AllowDefer && job.req.DefersLeft > 0 {
		st.combo = createControl(hwnd, utf16(comboClassName), "",
			uint32(wsChild)|wsVisible|wsTabStop|cbsDropdownLst|wsVScroll,
			dp(consentPad, dpi), dp(btnY+2, dpi), dp(96, dpi), dp(160, dpi), 0)
		sendMessage(st.combo, wmSetFont, uiFont(dpi, 10, fwNormal), 1)
		for _, opt := range deferOptions {
			sendMessageStr(st.combo, cbAddString, 0, fmt.Sprintf("%d 分鐘", opt))
		}
		sendMessage(st.combo, cbSetCurSel, 0, 0)
		deferBtn = s.button(hwnd, "延後", consentPad+96+8, btnY, 88, consentBtnH, idRebootDefer, false, 0, bsDefPushButt)
	}

	rebootStatesMu.Lock()
	rebootStates[hwnd] = st
	rebootStatesMu.Unlock()

	procShowWindow.Call(hwnd, swShow)
	procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWindow)
	procSetForegroundWindow.Call(hwnd)
	if deferBtn != 0 {
		procSetFocus.Call(deferBtn) // Enter postpones rather than reboots
	}
	procSetTimer.Call(hwnd, rebootTimerID, 1000, 0)
	u.log.Info("reboot prompt shown", "id", job.req.ID, "shutdown", job.req.Shutdown,
		"countdown_sec", remaining, "can_defer", deferBtn != 0)
}

func rebootTitle(shutdown bool) string {
	if shutdown {
		return "系統即將關機"
	}
	return "系統即將重新啟動"
}

func rebootText(req helperipc.RebootPrompt, remaining int) string {
	word := "重新啟動"
	if req.Shutdown {
		word = "關機"
	}
	return fmt.Sprintf("將於 %02d:%02d 後%s", remaining/60, remaining%60, word)
}

func rebootFinish(hwnd uintptr, action string, deferMin int) {
	rebootStatesMu.Lock()
	st := rebootStates[hwnd]
	if st == nil || st.done {
		rebootStatesMu.Unlock()
		return
	}
	st.done = true
	delete(rebootStates, hwnd)
	rebootStatesMu.Unlock()

	procKillTimer.Call(hwnd, rebootTimerID)
	if theUI != nil {
		theUI.log.Info("reboot prompt answered", "id", st.job.req.ID, "action", action, "defer_min", deferMin)
	}
	st.job.result <- helperipc.RebootResponse{ID: st.job.req.ID, Action: action, DeferMinutes: deferMin}
	procDestroyWindow.Call(hwnd)
}

func rebootWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	if r, ok := skinProc(hwnd, m, wp, lp); ok {
		return r
	}
	switch m {
	case wmTimer:
		rebootStatesMu.Lock()
		st := rebootStates[hwnd]
		rebootStatesMu.Unlock()
		if st == nil {
			return 0
		}
		st.remaining--
		if st.remaining <= 0 {
			rebootFinish(hwnd, helperipc.RebootTimeout, 0)
			return 0
		}
		setText(st.countdown, rebootText(st.job.req, st.remaining))
		st.skin.updateBar(hwnd, st.remaining)
		return 0
	case wmCommand:
		switch uint16(loword(wp)) {
		case idRebootNow:
			rebootFinish(hwnd, helperipc.RebootNow, 0)
		case idRebootDefer:
			rebootStatesMu.Lock()
			st := rebootStates[hwnd]
			rebootStatesMu.Unlock()
			mins := deferOptions[0]
			if st != nil && st.combo != 0 {
				sel := sendMessage(st.combo, cbGetCurSel, 0, 0)
				if int(sel) >= 0 && int(sel) < len(deferOptions) {
					mins = deferOptions[int(sel)]
				}
			}
			rebootFinish(hwnd, helperipc.RebootDefer, mins)
		}
		return 0
	case wmClose:
		// Closing without choosing is treated as a timeout (no action taken now).
		rebootFinish(hwnd, helperipc.RebootTimeout, 0)
		return 0
	case wmDestroy:
		rebootFinish(hwnd, helperipc.RebootTimeout, 0)
		dropSkin(hwnd)
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
