//go:build windows

package helper

import (
	"fmt"
	"sync"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

const (
	idRebootNow   = 1
	idRebootDefer = 2
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
	static    uintptr
	combo     uintptr
	remaining int
	shutdown  bool
	done      bool
}

var (
	rebootStates   = map[uintptr]*rebootState{}
	rebootStatesMu sync.Mutex
)

func (u *ui) openRebootWindow(job *rebootJob) {
	defer recoverPanic(u.log, "openRebootWindow")

	remaining := job.req.CountdownSec
	if remaining <= 0 {
		remaining = 300
	}
	const w, h = 460, 250
	hwnd := createTopWindow(rebootClass, rebootTitle(job.req.Shutdown),
		wsPopup|wsCaption|wsSysMenu, wsExTopmost, w, h)
	if hwnd == 0 {
		job.result <- helperipc.RebootResponse{ID: job.req.ID, Action: helperipc.RebootNow}
		return
	}

	st := &rebootState{job: job, remaining: remaining, shutdown: job.req.Shutdown}
	st.static = createControl(hwnd, staticClassName, rebootText(job.req, remaining),
		ssLeft|uint32(wsChild)|wsVisible, 20, 20, w-40, h-110, 0)

	nowLabel := "立即重新啟動"
	if job.req.Shutdown {
		nowLabel = "立即關機"
	}
	createControl(hwnd, buttonClassName, nowLabel,
		uint32(wsChild)|wsVisible|wsTabStop|bsDefPushButt, 30, h-60, 160, 32, idRebootNow)

	if job.req.AllowDefer && job.req.DefersLeft > 0 {
		st.combo = createControl(hwnd, utf16(comboClassName), "",
			uint32(wsChild)|wsVisible|wsTabStop|cbsDropdownLst|wsVScroll, 210, h-60, 100, 160, 0)
		for _, opt := range deferOptions {
			sendMessageStr(st.combo, cbAddString, 0, fmt.Sprintf("%d 分鐘", opt))
		}
		sendMessage(st.combo, cbSetCurSel, 0, 0)
		createControl(hwnd, buttonClassName, "延後",
			uint32(wsChild)|wsVisible|wsTabStop|bsPushButton, 320, h-60, 110, 32, idRebootDefer)
	}

	rebootStatesMu.Lock()
	rebootStates[hwnd] = st
	rebootStatesMu.Unlock()

	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	procSetTimer.Call(hwnd, rebootTimerID, 1000, 0)
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
	mm := remaining / 60
	ss := remaining % 60
	extra := ""
	if req.Message != "" {
		extra = "\n\n" + req.Message
	}
	return fmt.Sprintf("MIS 將於 %02d:%02d 後%s您的電腦，請儲存您的工作。%s", mm, ss, word, extra)
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
	st.job.result <- helperipc.RebootResponse{ID: st.job.req.ID, Action: action, DeferMinutes: deferMin}
	procDestroyWindow.Call(hwnd)
}

func rebootWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
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
		setText(st.static, rebootText(st.job.req, st.remaining))
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
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
