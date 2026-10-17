// Command nexmis-server is the NEXMIS central management service.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/winsvc"
	"github.com/tglobalcorp-mis/nexmis/internal/server/api"
	"github.com/tglobalcorp-mis/nexmis/internal/server/config"
	"github.com/tglobalcorp-mis/nexmis/internal/server/core"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
	"github.com/tglobalcorp-mis/nexmis/web"
)

// serviceName is the Windows service name used when the server runs on
// Windows Server.
const serviceName = "NexmisServer"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	resetAdmin := flag.String("reset-admin-password", "", "set a new password for the local admin account and exit")
	configFile := flag.String("config", "", "KEY=VALUE configuration file (same as NEXMIS_CONFIG)")
	logFile := flag.String("log-file", "", "write logs to this file instead of stdout")
	installSvc := flag.Bool("install-service", false, "Windows: register as the NexmisServer service (requires --config) and exit")
	uninstallSvc := flag.Bool("uninstall-service", false, "Windows: remove the NexmisServer service and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("nexmis-server", core.Version, core.BuildTime)
		return
	}
	if *configFile != "" {
		abs, err := filepath.Abs(*configFile)
		if err == nil {
			*configFile = abs
		}
		os.Setenv("NEXMIS_CONFIG", *configFile)
	}
	var err error
	switch {
	case *installSvc:
		err = installService(*configFile, *logFile)
	case *uninstallSvc:
		_ = winsvc.StopService(serviceName, 30*time.Second)
		err = winsvc.RemoveService(serviceName)
	default:
		err = winsvc.Run(serviceName, func(ctx context.Context) error {
			return run(ctx, *migrateOnly, *resetAdmin, *logFile)
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexmis-server:", err)
		os.Exit(1)
	}
}

func installService(configFile, logFile string) error {
	if configFile == "" {
		return errors.New("--install-service requires --config <file>")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if logFile == "" {
		logFile = filepath.Join(filepath.Dir(configFile), "logs", "nexmis-server.log")
	}
	if err := winsvc.InstallService(serviceName, "NEXMIS Server", "NEXMIS central management service",
		exe, "--config", configFile, "--log-file", logFile); err != nil {
		return err
	}
	return winsvc.StartService(serviceName)
}

func newLogger(level, format, file string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	out := os.Stdout
	if file != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		out = f
	}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(out, opts)), nil
	}
	return slog.New(slog.NewTextHandler(out, opts)), nil
}

func run(ctx context.Context, migrateOnly bool, resetAdmin, logFile string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := newLogger(cfg.LogLevel, cfg.LogFormat, logFile)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if migrateOnly {
		log.Info("migrations applied")
		return nil
	}
	svc, err := core.New(cfg, st, log)
	if err != nil {
		return err
	}
	if resetAdmin != "" {
		return svc.ResetAdminPassword(ctx, resetAdmin)
	}
	if err := svc.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	var ui fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			ui = sub
		} else {
			log.Warn("web UI not embedded; build web/ before the server for the admin console")
		}
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(svc, ui).Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	if cfg.TLSCert != "" {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	go svc.RunJobs(ctx)

	errc := make(chan error, 1)
	go func() {
		log.Info("nexmis-server listening", "addr", cfg.Listen, "public_url", cfg.PublicURL, "version", core.Version,
			"tls", cfg.TLSCert != "")
		var err error
		if cfg.TLSCert != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
