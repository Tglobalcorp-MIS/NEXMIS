//go:build !windows

package helperhost

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
)

// otherHost is the development Host: it listens on a Unix socket and treats any
// connecting process as the console helper. It does not launch the helper
// (the developer runs `nexmis-agent --user-helper` by hand).
type otherHost struct {
	base
	sockPath string
}

// New returns a development Host.
func New(log *slog.Logger, exePath string) Host {
	return &otherHost{
		base:     newBase(log, exePath),
		sockPath: filepath.Join(sys.DataDir(), "helper.sock"),
	}
}

func (h *otherHost) Run(ctx context.Context) {
	if err := os.MkdirAll(filepath.Dir(h.sockPath), 0o700); err != nil {
		h.log.Error("create data dir", "err", err)
		return
	}
	_ = os.Remove(h.sockPath)
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "unix", h.sockPath)
	if err != nil {
		h.log.Error("listen unix socket", "path", h.sockPath, "err", err)
		return
	}
	defer ln.Close()
	defer os.Remove(h.sockPath)
	h.log.Info("dev helper endpoint listening", "path", h.sockPath)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go h.serveConn(ctx, conn, 0, func(helperipc.Hello) bool { return true })
	}
}
