// Package helperhost runs on the agent service side (LocalSystem, session 0)
// and manages the user-session helper process: it launches the helper in the
// active console session, serves the local IPC endpoint (a named pipe on
// Windows, a Unix socket for development), verifies the peer, and relays
// messages. The service uses the Host interface to talk to whichever helper is
// currently attached to the console session.
package helperhost

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// ErrNoHelper is returned by Send when no console-session helper is attached.
var ErrNoHelper = errors.New("no user session helper")

// Host manages the user-session helper process and IPC.
type Host interface {
	// Run blocks, managing the helper, until ctx is done.
	Run(ctx context.Context)
	// Send delivers a message to the console-session helper.
	Send(t, id string, payload any) error
	// Events streams messages received from helpers (including THello).
	Events() <-chan helperipc.Envelope
	// Online reports whether the console-session helper is attached.
	Online() bool
	// User reports the console helper's "DOMAIN\user", or "".
	User() string
}

const (
	pingInterval = 15 * time.Second
	recvTimeout  = 30 * time.Second
	eventBuffer  = 64
)

// clientConn is one attached helper connection.
type clientConn struct {
	ipc       *helperipc.Conn
	sessionID uint32
	user      string
	pid       int
	console   bool
}

// base holds the shared Host state and per-connection servicing used by both
// the Windows and development implementations.
type base struct {
	log     *slog.Logger
	exePath string
	events  chan helperipc.Envelope

	mu      sync.Mutex
	console *clientConn // the attached console-session helper, if any
}

func newBase(log *slog.Logger, exePath string) base {
	if log == nil {
		log = slog.Default()
	}
	return base{
		log:     log.With("component", "helperhost"),
		exePath: exePath,
		events:  make(chan helperipc.Envelope, eventBuffer),
	}
}

func (b *base) Events() <-chan helperipc.Envelope { return b.events }

func (b *base) Online() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.console != nil
}

func (b *base) User() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.console == nil {
		return ""
	}
	return b.console.user
}

func (b *base) Send(t, id string, payload any) error {
	b.mu.Lock()
	c := b.console
	b.mu.Unlock()
	if c == nil {
		return ErrNoHelper
	}
	return c.ipc.Send(t, id, payload)
}

// setConsole registers c as the console helper if it is one.
func (b *base) setConsole(c *clientConn) {
	if !c.console {
		return
	}
	b.mu.Lock()
	b.console = c
	b.mu.Unlock()
}

// clearConsole removes c as the console helper if it is the current one.
func (b *base) clearConsole(c *clientConn) {
	b.mu.Lock()
	if b.console == c {
		b.console = nil
	}
	b.mu.Unlock()
}

// emit forwards an envelope to the events channel without blocking; if the
// consumer is behind, the oldest buffered event is dropped.
func (b *base) emit(e helperipc.Envelope) {
	for {
		select {
		case b.events <- e:
			return
		default:
			select {
			case <-b.events: // drop oldest, retry
				b.log.Warn("event buffer full; dropping oldest")
			default:
				return
			}
		}
	}
}

// serveConn services one helper connection until it disconnects or ctx ends.
// isConsole decides, from the first Hello, whether this connection is the
// console-session helper.
func (b *base) serveConn(ctx context.Context, nc net.Conn, sessionID uint32, isConsole func(h helperipc.Hello) bool) {
	ipc := helperipc.NewConn(nc)
	defer ipc.Close()

	c := &clientConn{ipc: ipc, sessionID: sessionID}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lastRecv := time.Now()
	var lastMu sync.Mutex
	touch := func() { lastMu.Lock(); lastRecv = time.Now(); lastMu.Unlock() }

	// Keepalive and liveness watchdog.
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				lastMu.Lock()
				idle := time.Since(lastRecv)
				lastMu.Unlock()
				if idle > recvTimeout {
					b.log.Warn("helper unresponsive; disconnecting", "session", sessionID)
					ipc.Close()
					cancel()
					return
				}
				if err := ipc.Send(helperipc.TPing, "", nil); err != nil {
					ipc.Close()
					cancel()
					return
				}
			}
		}
	}()

	for {
		env, err := ipc.Recv()
		if err != nil {
			break
		}
		touch()
		switch env.T {
		case helperipc.TPong:
			// keepalive only
		case helperipc.TPing:
			_ = ipc.Send(helperipc.TPong, env.ID, nil)
		case helperipc.THello:
			var h helperipc.Hello
			if derr := env.Decode(&h); derr == nil {
				c.user = h.User
				c.pid = h.PID
				if sessionID == 0 {
					c.sessionID = h.SessionID
				}
				c.console = isConsole(h)
				b.setConsole(c)
				b.log.Info("helper attached", "user", h.User, "session", c.sessionID,
					"pid", h.PID, "version", h.Version, "console", c.console)
			}
			b.emit(env)
		default:
			b.emit(env)
		}
		if ctx.Err() != nil {
			break
		}
	}
	b.clearConsole(c)
}
