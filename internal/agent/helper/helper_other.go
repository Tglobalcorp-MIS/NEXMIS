//go:build !windows

package helper

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
)

// Main is the development (non-Windows) helper: it connects to the service's
// Unix socket and drives a terminal interface so integration tests can run on
// Linux. It implements enough behaviour (consent via env var, chat/notify to
// stdout, run.script for real) to exercise the service end-to-end.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sockPath := filepath.Join(sys.DataDir(), "helper.sock")
	fmt.Printf("[helper] dev mode, version %s, connecting to %s\n", Version, sockPath)

	uname := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		uname = u.Username
	}
	hello := helperipc.Hello{User: uname, SessionID: 0, PID: os.Getpid(), Version: Version}

	for ctx.Err() == nil {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			fmt.Println("[helper] not connected (service offline); retrying in 3s")
			if !sleepCtx(ctx, 3*time.Second) {
				return 0
			}
			continue
		}
		fmt.Println("[helper] connected")
		ipc := helperipc.NewConn(conn)
		h := devHandlers(ipc)
		if err := runClient(ctx, ipc, hello, h); err != nil && ctx.Err() == nil {
			fmt.Printf("[helper] disconnected: %v\n", err)
		}
		if !sleepCtx(ctx, 3*time.Second) {
			return 0
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func devHandlers(ipc *helperipc.Conn) handlers {
	return handlers{
		Consent: func(ctx context.Context, req helperipc.ConsentRequest) helperipc.ConsentResult {
			if req.Policy == "notify_only" {
				fmt.Printf("[helper] (notify) remote %s session starting by %s\n", req.Mode, req.Requester)
				return helperipc.ConsentResult{SessionID: req.SessionID, Granted: true}
			}
			decision := strings.ToLower(strings.TrimSpace(os.Getenv("NEXMIS_DEV_CONSENT")))
			granted := decision == "allow"
			fmt.Printf("[helper] consent request from %s (%s): %q -> granted=%v (NEXMIS_DEV_CONSENT)\n",
				req.Requester, req.Mode, req.Reason, granted)
			reason := "denied"
			if granted {
				reason = ""
			}
			return helperipc.ConsentResult{SessionID: req.SessionID, Granted: granted, Reason: reason}
		},
		RemoteStart: func(msg helperipc.RemoteStart) {
			fmt.Printf("[helper] remote.start -> unsupported on this platform\n")
			_ = ipc.Send(helperipc.TRemoteEnded, "", helperipc.RemoteEnded{
				SessionID: msg.SessionID, By: "error",
				Reason: "screen capture not supported on this platform",
			})
		},
		RemoteStop: func(msg helperipc.RemoteStop) {
			fmt.Printf("[helper] remote.stop %s\n", msg.Reason)
		},
		Chat: func(msg helperipc.ChatMessage) {
			fmt.Printf("[chat] %s: %s\n", msg.Sender, msg.Body)
			_ = ipc.Send(helperipc.TChatAck, "", helperipc.IDs{IDs: []string{msg.MessageID}})
		},
		Notify: func(msg helperipc.Notify) {
			fmt.Printf("[notify:%s] %s - %s\n", msg.Level, msg.Title, msg.Message)
		},
		Reboot: func(req helperipc.RebootPrompt) helperipc.RebootResponse {
			word := "reboot"
			if req.Shutdown {
				word = "shutdown"
			}
			fmt.Printf("[helper] %s in %ds: %s\n", word, req.CountdownSec, req.Message)
			return helperipc.RebootResponse{ID: req.ID, Action: helperipc.RebootNow}
		},
		Run:  runScript,
		Lock: func() { fmt.Println("[helper] lock workstation (noop in dev)") },
		Status: func(st helperipc.Status) {
			fmt.Printf("[status] connected=%v server=%s device=%s\n", st.Connected, st.ServerURL, st.DeviceName)
		},
	}
}
