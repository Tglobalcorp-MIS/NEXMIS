package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func (a *Agent) handle(ctx context.Context, env protocol.Envelope) {
	switch env.T {
	case protocol.TConfig:
		var c protocol.AgentConfig
		if env.Decode(&c) == nil {
			a.applyConfig(c)
		}
	case protocol.TCommand:
		var sc protocol.SignedCommand
		if env.Decode(&sc) == nil {
			a.onCommand(sc)
		}
	case protocol.TInventoryRequest:
		var r protocol.InventoryRequest
		if env.Decode(&r) == nil {
			a.invMu.Lock()
			delete(a.invHash, r.Kind)
			a.invMu.Unlock()
			a.requestRefresh(r.Kind)
		}
	case protocol.TChat:
		var m protocol.Chat
		if env.Decode(&m) == nil {
			a.deliverChat(m)
		}
	case protocol.TChatRead:
		var r protocol.ChatRead
		if env.Decode(&r) == nil {
			_ = a.host.Send(helperipc.TChatSeen, "", helperipc.IDs{IDs: r.MessageIDs})
		}
	case protocol.TRemoteRequest:
		var sc protocol.SignedCommand
		if env.Decode(&sc) == nil {
			a.onRemoteRequest(sc)
		}
	case protocol.TRemoteStop:
		var rs protocol.RemoteStop
		if env.Decode(&rs) == nil {
			a.pendingMu.Lock()
			delete(a.remote, rs.SessionID)
			a.pendingMu.Unlock()
			_ = a.host.Send(helperipc.TRemoteStop, "", helperipc.RemoteStop{SessionID: rs.SessionID, Reason: rs.Reason})
		}
	case protocol.TCommandResultAck:
		a.onResultAck(env.Ref)
	case protocol.TPing:
		a.send(protocol.TPong, nil)
	}
}

// verify checks a signed message from the server and claims its id.
func (a *Agent) verify(sc protocol.SignedCommand) (protocol.Command, string) {
	c, err := protocol.VerifyCommand(ed25519.PublicKey(a.cfg.ServerSignKey), a.cfg.AgentID, sc, time.Now())
	if err != nil {
		a.log.Warn("rejected command", "err", err)
		return c, err.Error()
	}
	if !a.executed.Claim(c.CommandID) {
		return c, "duplicate"
	}
	return c, ""
}

func (a *Agent) onCommand(sc protocol.SignedCommand) {
	c, reason := a.verify(sc)
	if reason == "duplicate" {
		// Already executed (or running); the result is in the outbox.
		a.flushOutbox()
		return
	}
	if reason != "" {
		if c.CommandID != "" {
			a.send(protocol.TCommandAck, protocol.CommandAck{CommandID: c.CommandID, Accepted: false, Reason: "簽章驗證失敗：" + reason})
		}
		return
	}
	go func() {
		// Power actions and long installs should not block quick commands,
		// but the overall number of concurrent jobs is bounded. The ack is
		// sent once a slot is free, since the server starts the command's
		// timeout from it.
		a.cmdSem <- struct{}{}
		defer func() { <-a.cmdSem }()
		a.send(protocol.TCommandAck, protocol.CommandAck{CommandID: c.CommandID, Accepted: true})
		a.log.Info("executing command", "id", c.CommandID, "kind", c.Kind, "by", c.IssuedBy)
		start := time.Now()
		res := a.execute(a.life, c)
		res.CommandID = c.CommandID
		if res.StartedAt.IsZero() {
			res.StartedAt = start
		}
		if res.FinishedAt.IsZero() {
			res.FinishedAt = time.Now()
		}
		a.log.Info("command finished", "id", c.CommandID, "kind", c.Kind, "status", res.Status, "exit", res.ExitCode)
		a.sendResult(res)
		a.executed.Done(c.CommandID)
		if res.Status == protocol.StatusSucceeded && (c.Kind == protocol.CmdReboot || c.Kind == protocol.CmdShutdown) {
			a.finishPowerAction(c)
		}
	}()
}

// ---- chat ----

func (a *Agent) deliverChat(m protocol.Chat) {
	if a.host.Online() {
		if err := a.host.Send(helperipc.TChatMessage, m.MessageID, helperipc.ChatMessage{
			MessageID: m.MessageID, Sender: m.Sender, Body: m.Body, SentAt: m.SentAt}); err == nil {
			return
		}
	}
	a.pendingMu.Lock()
	if len(a.chatQueue) < 200 {
		a.chatQueue = append(a.chatQueue, m)
	}
	a.pendingMu.Unlock()
}

func (a *Agent) flushChatQueue() {
	a.pendingMu.Lock()
	q := a.chatQueue
	a.chatQueue = nil
	a.pendingMu.Unlock()
	for _, m := range q {
		a.deliverChat(m)
	}
}

// ---- remote ----

func (a *Agent) onRemoteRequest(sc protocol.SignedCommand) {
	c, reason := a.verify(sc)
	if reason != "" || c.Kind != "remote" {
		a.log.Warn("remote request ignored", "id", c.CommandID, "kind", c.Kind, "reason", reason)
		return
	}
	var req protocol.RemoteRequest
	if err := json.Unmarshal(c.Args, &req); err != nil {
		a.log.Warn("remote request ignored: bad args", "id", c.CommandID, "err", err)
		return
	}
	a.log.Info("remote request received", "session", req.SessionID, "mode", req.Mode,
		"requester", req.Requester, "policy", req.Policy)
	consent := func(granted bool, why, user string) {
		a.log.Info("remote consent sent", "session", req.SessionID, "granted", granted, "reason", why)
		a.send(protocol.TRemoteConsent, protocol.RemoteConsent{SessionID: req.SessionID, Granted: granted, Reason: why, User: user})
	}
	if req.Policy == protocol.ConsentDeny {
		consent(false, "policy", "")
		return
	}
	if !a.host.Online() {
		consent(false, "no_user", "")
		return
	}
	a.pendingMu.Lock()
	a.remote[req.SessionID] = req
	a.pendingMu.Unlock()
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = a.config().ConsentTimeout
	}
	if err := a.host.Send(helperipc.TConsentRequest, req.SessionID, helperipc.ConsentRequest{
		SessionID: req.SessionID, Mode: req.Mode, Requester: req.Requester, Reason: req.Reason,
		TimeoutSec: timeout, Policy: req.Policy,
	}); err != nil {
		a.log.Warn("consent request not delivered to helper", "session", req.SessionID, "err", err)
		consent(false, "helper_unavailable", "")
		return
	}
	a.log.Info("consent request sent to helper", "session", req.SessionID, "timeout_sec", timeout)
}

func (a *Agent) onConsentResult(r helperipc.ConsentResult) {
	a.pendingMu.Lock()
	req, ok := a.remote[r.SessionID]
	if !r.Granted {
		delete(a.remote, r.SessionID)
	}
	a.pendingMu.Unlock()
	if !ok {
		a.log.Warn("consent result for unknown session", "session", r.SessionID, "granted", r.Granted)
		return
	}
	user := r.User
	if user == "" {
		user = a.host.User()
	}
	a.log.Info("consent result from helper", "session", r.SessionID, "granted", r.Granted,
		"reason", r.Reason, "user", user)
	a.send(protocol.TRemoteConsent, protocol.RemoteConsent{SessionID: r.SessionID, Granted: r.Granted, Reason: r.Reason, User: user})
	if !r.Granted {
		return
	}
	_ = a.host.Send(helperipc.TRemoteStart, r.SessionID, helperipc.RemoteStart{
		SessionID: r.SessionID, Mode: req.Mode, Requester: req.Requester,
		URL: a.cfg.WSURL(req.RelayURL), Token: req.RelayToken, TLS: a.cfg.TLS,
	})
}
