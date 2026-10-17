package helper

import (
	"context"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// handlers are the callbacks the platform (or the dev terminal) supplies to
// react to messages from the service. Request/response messages (consent,
// reboot, run) return the reply, which runClient sends back; the rest are
// fire-and-forget. Any handler may be nil.
type handlers struct {
	Consent     func(ctx context.Context, req helperipc.ConsentRequest) helperipc.ConsentResult
	RemoteStart func(msg helperipc.RemoteStart)
	RemoteStop  func(msg helperipc.RemoteStop)
	Chat        func(msg helperipc.ChatMessage)
	ChatSeen    func(ids helperipc.IDs)
	Notify      func(msg helperipc.Notify)
	Reboot      func(req helperipc.RebootPrompt) helperipc.RebootResponse
	Run         func(ctx context.Context, req helperipc.RunScript) helperipc.RunResult
	Lock        func()
	Status      func(st helperipc.Status)
}

// helperPingInterval is how often the helper proactively pings the service.
const helperPingInterval = 20 * time.Second

// runClient sends the Hello then services the connection until it closes or ctx
// is cancelled. Request handlers run in their own goroutines so a modal dialog
// never blocks message processing; helperipc.Conn.Send is concurrency-safe.
func runClient(ctx context.Context, conn *helperipc.Conn, hello helperipc.Hello, h handlers) error {
	if err := conn.Send(helperipc.THello, "", hello); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		t := time.NewTicker(helperPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := conn.Send(helperipc.TPing, "", nil); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Close the connection when ctx ends so Recv unblocks.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		env, err := conn.Recv()
		if err != nil {
			return err
		}
		dispatch(ctx, conn, env, h)
	}
}

func dispatch(ctx context.Context, conn *helperipc.Conn, env helperipc.Envelope, h handlers) {
	switch env.T {
	case helperipc.TPing:
		_ = conn.Send(helperipc.TPong, env.ID, nil)
	case helperipc.TPong:
		// keepalive

	case helperipc.TConsentRequest:
		var req helperipc.ConsentRequest
		if env.Decode(&req) == nil && h.Consent != nil {
			id := env.ID
			go func() {
				res := h.Consent(ctx, req)
				_ = conn.Send(helperipc.TConsentResult, id, res)
			}()
		}

	case helperipc.TRemoteStart:
		var msg helperipc.RemoteStart
		if env.Decode(&msg) == nil && h.RemoteStart != nil {
			go h.RemoteStart(msg)
		}

	case helperipc.TRemoteStop:
		var msg helperipc.RemoteStop
		if env.Decode(&msg) == nil && h.RemoteStop != nil {
			go h.RemoteStop(msg)
		}

	case helperipc.TChatMessage:
		var msg helperipc.ChatMessage
		if env.Decode(&msg) == nil && h.Chat != nil {
			h.Chat(msg)
		}

	case helperipc.TChatSeen:
		var ids helperipc.IDs
		if env.Decode(&ids) == nil && h.ChatSeen != nil {
			h.ChatSeen(ids)
		}

	case helperipc.TNotify:
		var msg helperipc.Notify
		if env.Decode(&msg) == nil && h.Notify != nil {
			h.Notify(msg)
		}

	case helperipc.TRebootPrompt:
		var req helperipc.RebootPrompt
		if env.Decode(&req) == nil && h.Reboot != nil {
			id := env.ID
			go func() {
				res := h.Reboot(req)
				_ = conn.Send(helperipc.TRebootResponse, id, res)
			}()
		}

	case helperipc.TRunScript:
		var req helperipc.RunScript
		if env.Decode(&req) == nil && h.Run != nil {
			id := env.ID
			go func() {
				res := h.Run(ctx, req)
				_ = conn.Send(helperipc.TRunResult, id, res)
			}()
		}

	case helperipc.TLock:
		if h.Lock != nil {
			go h.Lock()
		}

	case helperipc.TStatus:
		var st helperipc.Status
		if env.Decode(&st) == nil && h.Status != nil {
			h.Status(st)
		}
	}
}
