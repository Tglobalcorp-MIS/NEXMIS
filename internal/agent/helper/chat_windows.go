//go:build windows

package helper

import (
	"strings"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

const (
	idChatSend  = 1
	emSetSel    = 0x00B1
	emScrollCar = 0x00B7
	emReplaceSl = 0x00C2
	wmSize      = 0x0005
)

// chat child handles (one chat window at a time).
type chatWin struct {
	logEdit uintptr
	input   uintptr
}

var theChat chatWin

// openChat shows (or focuses) the chat window on the UI thread.
func (u *ui) openChat() {
	defer recoverPanic(u.log, "openChat")
	if u.chatHwnd != 0 {
		procShowWindow.Call(u.chatHwnd, swShow)
		procSetForegroundWindow.Call(u.chatHwnd)
		u.markChatRead()
		return
	}
	const w, h = 460, 420
	hwnd := createTopWindow(chatClass, "NEXMIS — 與 MIS 對話",
		wsOverlappedWn, 0, w, h)
	if hwnd == 0 {
		return
	}
	u.chatHwnd = hwnd
	theChat.logEdit = createControl(hwnd, editClassName, "",
		uint32(wsChild)|wsVisible|wsBorder|esMultiline|esReadonly|esAutoVScroll|wsVScroll,
		10, 10, w-35, h-110, 0)
	theChat.input = createControl(hwnd, editClassName, "",
		uint32(wsChild)|wsVisible|wsBorder|esAutoHScroll, 10, h-90, w-150, 28, 0)
	createControl(hwnd, buttonClassName, "送出",
		uint32(wsChild)|wsVisible|wsTabStop|bsDefPushButt, w-130, h-91, 100, 30, idChatSend)

	u.renderChatLog()
	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	u.markChatRead()
}

// handleIncomingChat is called on the UI thread for a message from MIS.
func (u *ui) handleIncomingChat(cm helperipc.ChatMessage) {
	u.chatMu.Lock()
	u.chatLog = append(u.chatLog, chatLine{sender: cm.Sender, body: cm.Body, fromMe: cm.FromUser})
	if len(u.chatLog) > 200 {
		u.chatLog = u.chatLog[len(u.chatLog)-200:]
	}
	u.chatMu.Unlock()

	// Acknowledge that the message was received by the helper.
	if u.sendIPC != nil && !cm.FromUser {
		_ = u.sendIPC(helperipc.TChatAck, "", helperipc.IDs{IDs: []string{cm.MessageID}})
	}

	// Track it as unread until the user has the chat window open.
	if !cm.FromUser && cm.MessageID != "" {
		u.unreadChatIDs = append(u.unreadChatIDs, cm.MessageID)
	}
	if u.chatHwnd != 0 {
		u.renderChatLog()
		u.markChatRead()
	} else if !cm.FromUser {
		// Not open: nudge without stealing focus.
		u.showBalloon("MIS 訊息", cm.Body, "info")
	}
}

// markChatRead sends a read receipt for every MIS message shown since the
// last receipt.
func (u *ui) markChatRead() {
	if u.sendIPC != nil && len(u.unreadChatIDs) > 0 {
		_ = u.sendIPC(helperipc.TChatRead, "", helperipc.IDs{IDs: u.unreadChatIDs})
		u.unreadChatIDs = nil
	}
}

// renderChatLog rebuilds the read-only transcript and scrolls to the bottom.
func (u *ui) renderChatLog() {
	if theChat.logEdit == 0 {
		return
	}
	u.chatMu.Lock()
	var b strings.Builder
	for _, l := range u.chatLog {
		who := l.sender
		if l.fromMe {
			who = "我"
		}
		b.WriteString(who)
		b.WriteString("： ")
		b.WriteString(l.body)
		b.WriteString("\r\n")
	}
	u.chatMu.Unlock()
	setText(theChat.logEdit, b.String())
	// Scroll to end.
	n := sendMessage(theChat.logEdit, 0x000E /* WM_GETTEXTLENGTH */, 0, 0)
	sendMessage(theChat.logEdit, emSetSel, n, n)
	sendMessage(theChat.logEdit, emScrollCar, 0, 0)
}

func chatWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	u := theUI
	switch m {
	case wmCommand:
		if uint16(loword(wp)) == idChatSend && u != nil {
			body := strings.TrimSpace(getText(theChat.input))
			if body != "" {
				u.chatMu.Lock()
				u.chatLog = append(u.chatLog, chatLine{sender: "我", body: body, fromMe: true})
				u.chatMu.Unlock()
				setText(theChat.input, "")
				u.renderChatLog()
				if u.onChatSend != nil {
					u.onChatSend(body)
				}
			}
		}
		return 0
	case wmClose:
		if u != nil {
			u.chatHwnd = 0
		}
		theChat = chatWin{}
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if u != nil {
			u.chatHwnd = 0
		}
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
