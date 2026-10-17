package agent

import (
	"context"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// helperLoop processes messages from user-session helpers.
func (a *Agent) helperLoop(ctx context.Context) {
	events := a.host.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-events:
			if !ok {
				return
			}
			a.onHelperMessage(env)
		}
	}
}

func (a *Agent) sendHelperStatus() {
	name := ""
	if id, err := a.col.Identity(context.Background()); err == nil {
		name = id.Hostname
	}
	_ = a.host.Send(helperipc.TStatus, "", helperipc.Status{
		Connected: a.connected.Load(), ServerURL: a.cfg.ServerURL, DeviceName: name, Version: a.opt.Version,
	})
}

func (a *Agent) onHelperMessage(env helperipc.Envelope) {
	switch env.T {
	case helperipc.THello:
		var h helperipc.Hello
		_ = env.Decode(&h)
		a.log.Info("user helper connected", "user", h.User, "session", h.SessionID, "version", h.Version)
		a.sendHelperStatus()
		a.flushChatQueue()
		// Heartbeat promptly so the console shows the helper as online.
		a.requestRefresh("heartbeat")

	case helperipc.TConsentResult:
		var r helperipc.ConsentResult
		if env.Decode(&r) == nil {
			a.onConsentResult(r)
		}

	case helperipc.TRemoteEnded:
		var r helperipc.RemoteEnded
		if env.Decode(&r) == nil {
			a.pendingMu.Lock()
			delete(a.remote, r.SessionID)
			a.pendingMu.Unlock()
			a.send(protocol.TRemoteStop, protocol.RemoteStop{SessionID: r.SessionID, By: r.By, Reason: r.Reason})
		}

	case helperipc.TChatSend:
		var m helperipc.ChatSend
		if env.Decode(&m) == nil && m.Body != "" {
			sender := m.User
			if sender == "" {
				sender = a.host.User()
			}
			a.sendUserChat(protocol.Chat{From: "user", Sender: sender, Body: m.Body, SentAt: time.Now().UTC()})
		}

	case helperipc.TChatAck, helperipc.TChatRead:
		var ids helperipc.IDs
		if env.Decode(&ids) == nil && len(ids.IDs) > 0 {
			t := protocol.TChatAck
			if env.T == helperipc.TChatRead {
				t = protocol.TChatRead
			}
			a.send(t, protocol.ChatRead{MessageIDs: ids.IDs})
		}

	case helperipc.TRebootResponse, helperipc.TRunResult:
		a.pendingMu.Lock()
		ch := a.pendingRun[env.ID]
		a.pendingMu.Unlock()
		if ch != nil {
			select {
			case ch <- env.P:
			default:
			}
		}

	case helperipc.TSAS:
		if err := sys.SendSAS(); err != nil {
			a.log.Warn("SendSAS failed", "err", err)
		}
	}
}

// maxUserChatQueue bounds user messages held while the server is
// unreachable.
const maxUserChatQueue = 200

// sendUserChat sends a message typed by the user, holding it in memory
// while the agent is offline so it reaches MIS after reconnecting.
func (a *Agent) sendUserChat(m protocol.Chat) {
	a.pendingMu.Lock()
	if len(a.chatOut) < maxUserChatQueue {
		a.chatOut = append(a.chatOut, m)
	}
	a.pendingMu.Unlock()
	a.flushUserChat()
}

// flushUserChat sends queued user messages in order, stopping at the
// first one that cannot be sent.
func (a *Agent) flushUserChat() {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	for len(a.chatOut) > 0 {
		if !a.connected.Load() || !a.send(protocol.TChat, a.chatOut[0]) {
			return
		}
		a.chatOut = a.chatOut[1:]
	}
}
