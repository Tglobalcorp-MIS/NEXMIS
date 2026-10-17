//go:build windows

package helperhost

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
	"unsafe"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

const pipeName = `\\.\pipe\nexmis-agent-helper`

// pipeSDDL grants full control to SYSTEM and read/write to interactive users
// (the logged-on helper runs as the interactive user).
const pipeSDDL = "D:P(A;;FA;;;SY)(A;;FRFW;;;IU)"

const noSession = 0xFFFFFFFF

// winHost is the production Host.
type winHost struct {
	base

	mon struct {
		sync.Mutex
		session uint32         // console session a helper was launched for
		proc    windows.Handle // launched helper process handle
		backoff time.Duration
	}
}

// New returns the Windows Host.
func New(log *slog.Logger, exePath string) Host {
	h := &winHost{base: newBase(log, exePath)}
	h.mon.session = noSession
	h.mon.backoff = 5 * time.Second
	return h
}

func (h *winHost) Run(ctx context.Context) {
	go h.acceptLoop(ctx)
	h.monitorLoop(ctx)
	h.killHelper()
}

// acceptLoop serves the named pipe, verifying each peer before servicing it.
func (h *winHost) acceptLoop(ctx context.Context) {
	ln, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		MessageMode:        false,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		h.log.Error("listen named pipe", "err", err)
		return
	}
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Warn("pipe accept failed", "err", err)
			continue
		}
		session, ok := h.verifyPeer(conn)
		if !ok {
			conn.Close()
			continue
		}
		go h.serveConn(ctx, conn, session, func(helperipc.Hello) bool {
			return windows.WTSGetActiveConsoleSessionId() == session
		})
	}
}

// verifyPeer checks the pipe client is our own executable and returns its
// session id.
func (h *winHost) verifyPeer(conn net.Conn) (uint32, bool) {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		h.log.Warn("pipe connection has no handle; rejecting")
		return 0, false
	}
	handle := windows.Handle(fd.Fd())
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(handle, &pid); err != nil {
		h.log.Warn("GetNamedPipeClientProcessId failed", "err", err)
		return 0, false
	}
	path, err := processImagePath(pid)
	if err != nil {
		h.log.Warn("resolve client image path failed", "pid", pid, "err", err)
		return 0, false
	}
	if !strings.EqualFold(path, h.exePath) {
		h.log.Warn("rejecting impostor helper", "pid", pid, "path", path, "want", h.exePath)
		return 0, false
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(pid, &session); err != nil {
		h.log.Warn("ProcessIdToSessionId failed", "pid", pid, "err", err)
		return 0, false
	}
	return session, true
}

func processImagePath(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// monitorLoop polls the active console session every 3s and keeps a helper
// running in it, restarting with backoff and killing the old helper on a
// session switch or logoff.
func (h *winHost) monitorLoop(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		h.reconcile()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *winHost) reconcile() {
	console := windows.WTSGetActiveConsoleSessionId()

	h.mon.Lock()
	launchedFor := h.mon.session
	proc := h.mon.proc
	h.mon.Unlock()

	// Console session gone or switched away from the one we launched for.
	if console == noSession || (proc != 0 && launchedFor != console) {
		if proc != 0 {
			h.killHelper()
		}
		if console == noSession {
			return
		}
	}

	// Is a user logged on to the console session?
	var token windows.Token
	if err := windows.WTSQueryUserToken(console, &token); err != nil {
		// No interactive user yet (e.g. at the logon screen).
		return
	}

	// Has the launched helper exited?
	h.mon.Lock()
	proc = h.mon.proc
	alive := proc != 0 && processAlive(proc)
	if !alive {
		if proc != 0 {
			windows.CloseHandle(proc)
			h.mon.proc = 0
		}
	}
	backoff := h.mon.backoff
	needLaunch := h.mon.proc == 0
	h.mon.Unlock()

	if !needLaunch {
		token.Close()
		return
	}

	ph, err := h.launchHelper(token, console)
	token.Close()
	if err != nil {
		h.log.Warn("launch helper failed; backing off", "err", err, "backoff", backoff)
		time.Sleep(backoff)
		h.mon.Lock()
		h.mon.backoff = nextBackoff(h.mon.backoff)
		h.mon.Unlock()
		return
	}
	h.mon.Lock()
	h.mon.proc = ph
	h.mon.session = console
	h.mon.backoff = 5 * time.Second
	h.mon.Unlock()
	h.log.Info("helper launched", "session", console)
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func processAlive(p windows.Handle) bool {
	ev, err := windows.WaitForSingleObject(p, 0)
	if err != nil {
		return false
	}
	return ev == uint32(windows.WAIT_TIMEOUT)
}

func (h *winHost) killHelper() {
	h.mon.Lock()
	proc := h.mon.proc
	h.mon.proc = 0
	h.mon.session = noSession
	h.mon.Unlock()
	if proc != 0 {
		windows.TerminateProcess(proc, 0)
		windows.CloseHandle(proc)
	}
}

// launchHelper starts the helper in the given session as the logged-on user.
func (h *winHost) launchHelper(token windows.Token, session uint32) (windows.Handle, error) {
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		return 0, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	cmdline := fmt.Sprintf("%q --user-helper --pipe %s", h.exePath, pipeName)
	cmdPtr, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return 0, err
	}
	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return 0, err
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Desktop = desktop
	si.Flags = windows.STARTF_USESHOWWINDOW
	si.ShowWindow = windows.SW_HIDE

	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcessAsUser(token, nil, cmdPtr, nil, nil, false,
		flags, env, nil, &si, &pi); err != nil {
		return 0, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}
