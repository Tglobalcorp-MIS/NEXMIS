//go:build windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"sync"
	"time"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

var errNotConnected = errors.New("helper: not connected to service")

const defaultPipeName = `\\.\pipe\nexmis-agent-helper`

// helperApp is the Windows helper: it owns the IPC connection to the service
// and the one active remote session, and bridges service messages to the UI.
type helperApp struct {
	log *slog.Logger
	ui  *ui

	mu           sync.Mutex
	conn         *helperipc.Conn
	remoteCancel context.CancelFunc
	endBy        string
}

// Main is the Windows helper entry point (nexmis-agent.exe --user-helper).
func Main(args []string) int {
	// Per-monitor DPI v2 so captures and UI are not virtualised/blurred.
	procSetProcessDpiAwareness2.Call(dpiPerMonitorV2)

	log := newLogger()
	defer recoverPanic(log, "Main")
	log.Info("helper starting", "version", Version)

	pipe := parsePipeArg(args)

	app := &helperApp{log: log, ui: newUI(log)}
	app.ui.curUser = currentUserName()
	app.wireUI()

	go app.pipeLoop(context.Background(), pipe)

	app.ui.run() // blocks on the UI message loop until the process exits
	return 0
}

func parsePipeArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--pipe" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return defaultPipeName
}

func currentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USERNAME")
}

func currentSessionID() uint32 {
	var sess uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &sess); err == nil {
		return sess
	}
	return 0
}

func (app *helperApp) wireUI() {
	u := app.ui
	u.sendIPC = app.send
	u.onContact = u.openChat
	u.onChatSend = func(body string) {
		_ = app.send(helperipc.TChatSend, "", helperipc.ChatSend{Body: body, User: u.user()})
	}
	u.onBannerEnd = app.bannerDisconnect
}

// send writes to the current IPC connection, if any.
func (app *helperApp) send(t, id string, p any) error {
	app.mu.Lock()
	conn := app.conn
	app.mu.Unlock()
	if conn == nil {
		return errNotConnected
	}
	return conn.Send(t, id, p)
}

// pipeLoop connects to the service's named pipe, reconnecting every 3s. While
// disconnected the tray shows "not connected".
func (app *helperApp) pipeLoop(ctx context.Context, pipe string) {
	defer recoverPanic(app.log, "pipeLoop")
	hello := helperipc.Hello{
		User:      app.ui.user(),
		SessionID: currentSessionID(),
		PID:       os.Getpid(),
		Version:   Version,
	}
	for ctx.Err() == nil {
		app.ui.setStatus(helperipc.Status{Connected: false})
		nc, err := winio.DialPipe(pipe, durPtr(5*time.Second))
		if err != nil {
			if !sleepCtxW(ctx, 3*time.Second) {
				return
			}
			continue
		}
		conn := helperipc.NewConn(nc)
		app.mu.Lock()
		app.conn = conn
		app.mu.Unlock()
		app.log.Info("connected to service pipe")

		err = runClient(ctx, conn, hello, app.handlers())
		app.log.Warn("pipe client ended", "err", err)

		app.mu.Lock()
		app.conn = nil
		app.mu.Unlock()
		if !sleepCtxW(ctx, 3*time.Second) {
			return
		}
	}
}

func durPtr(d time.Duration) *time.Duration { return &d }

func (app *helperApp) handlers() handlers {
	u := app.ui
	return handlers{
		Consent: func(ctx context.Context, req helperipc.ConsentRequest) helperipc.ConsentResult {
			if req.Policy == "notify_only" {
				u.notify(helperipc.Notify{
					Title:   "遠端連線",
					Message: fmt.Sprintf("%s 開始%s您的畫面", req.Requester, modeVerb(req.Mode)),
					Level:   "info",
				})
				return helperipc.ConsentResult{SessionID: req.SessionID, Granted: true, User: u.user()}
			}
			return u.requestConsent(req)
		},
		RemoteStart: app.onRemoteStart,
		RemoteStop:  app.onRemoteStop,
		Chat:        u.incomingChat,
		Notify:      u.notify,
		Reboot:      u.requestReboot,
		Run:         runScript,
		Lock:        func() { procLockWorkStation.Call() },
		Status:      u.setStatus,
	}
}

func (app *helperApp) onRemoteStart(msg helperipc.RemoteStart) {
	defer recoverPanic(app.log, "onRemoteStart")
	app.mu.Lock()
	if app.remoteCancel != nil {
		app.mu.Unlock()
		return // already streaming
	}
	ctx, cancel := context.WithCancel(context.Background())
	app.remoteCancel = cancel
	app.endBy = ""
	app.mu.Unlock()

	app.ui.showBanner(msg.Mode, msg.Requester)

	by, reason := runRemote(ctx, app.log, msg, app.sendCAD)

	app.mu.Lock()
	if app.endBy != "" {
		by, reason = app.endBy, ""
	}
	app.remoteCancel = nil
	app.mu.Unlock()

	app.ui.hideBanner()
	_ = app.send(helperipc.TRemoteEnded, "", helperipc.RemoteEnded{
		SessionID: msg.SessionID, By: by, Reason: reason,
	})
}

func (app *helperApp) onRemoteStop(msg helperipc.RemoteStop) {
	app.mu.Lock()
	c := app.remoteCancel
	app.mu.Unlock()
	if c != nil {
		c()
	}
}

func (app *helperApp) bannerDisconnect() {
	app.mu.Lock()
	app.endBy = "user"
	c := app.remoteCancel
	app.mu.Unlock()
	if c != nil {
		c()
	}
}

func (app *helperApp) sendCAD() {
	_ = app.send(helperipc.TSAS, "", nil)
}
