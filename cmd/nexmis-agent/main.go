// Command nexmis-agent is the NEXMIS endpoint agent.
//
// Usage:
//
//	nexmis-agent install --server https://mis.corp.local --token nxe_xxx [--ca ca.pem] [--pin sha256]
//	nexmis-agent uninstall
//	nexmis-agent run              # foreground (development / troubleshooting)
//	nexmis-agent status
//	nexmis-agent collect          # print collected inventory as JSON
//	nexmis-agent version
//
// When started by the Service Control Manager it runs as the service. The
// service starts "nexmis-agent --user-helper" in the logged-on user's
// session for UI, chat and screen sharing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/agent"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/collect"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/config"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/helper"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperhost"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/winsvc"
)

// Version is set at build time via -ldflags "-X main.Version=1.0.0".
var Version = "dev"

const (
	serviceName = "NexmisAgent"
	displayName = "NEXMIS Agent"
	description = "NEXMIS 企業 MIS 整合管理平台端點代理程式（盤點、健康監控、遠端協助）"
	exeName     = "nexmis-agent.exe"
)

func main() {
	helper.Version = Version
	if len(os.Args) > 1 && os.Args[1] == "--user-helper" {
		os.Exit(helper.Main(os.Args[2:]))
	}
	if winsvc.IsService() {
		if err := runAgent(true); err != nil {
			os.Exit(1)
		}
		return
	}
	cmd := "help"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "install":
		err = install(os.Args[2:])
	case "uninstall":
		err = uninstall(os.Args[2:])
	case "run":
		err = runAgent(false)
	case "status":
		err = status()
	case "collect":
		err = collectDump()
	case "version", "--version", "-v":
		fmt.Println("nexmis-agent", Version, runtime.GOOS+"/"+runtime.GOARCH)
	default:
		fmt.Fprintln(os.Stderr, strings.TrimSpace(`
NEXMIS Agent `+Version+`

用法：
  nexmis-agent install --server <URL> --token <註冊權杖> [--ca <CA 憑證 PEM 檔>] [--pin <憑證 SHA-256 指紋>]
  nexmis-agent uninstall [--purge]
  nexmis-agent status
  nexmis-agent run          前景執行（除錯用）
  nexmis-agent collect      輸出本機收集到的資料（JSON）
  nexmis-agent version`))
		if cmd != "help" && cmd != "-h" && cmd != "--help" {
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "錯誤：", err)
		os.Exit(1)
	}
}

// logger writes to DataDir\logs\agent.log (rotated at 5 MB) and, in the
// foreground, to stderr.
func logger(foreground bool) *slog.Logger {
	var w io.Writer = os.Stderr
	dir := filepath.Join(sys.DataDir(), "logs")
	if err := sys.SecureDir(dir); err == nil {
		if f, err := newRotatingFile(filepath.Join(dir, "agent.log"), 5<<20); err == nil {
			if foreground {
				w = io.MultiWriter(os.Stderr, f)
			} else {
				w = f
			}
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func runAgent(asService bool) error {
	log := logger(!asService)
	return winsvc.Run(serviceName, func(ctx context.Context) error {
		sys.CleanupOldBinary()
		dataDir := sys.DataDir()
		if err := sys.SecureDir(dataDir); err != nil {
			log.Error("prepare data dir", "err", err)
			return err
		}
		cfg, err := config.Load(dataDir)
		if err != nil {
			log.Error("load config", "err", err)
			return err
		}
		if cfg.ServerURL == "" {
			err := errors.New("尚未設定伺服器位址，請以 install --server ... --token ... 安裝")
			log.Error(err.Error())
			return err
		}
		exe, _ := os.Executable()
		a, err := agent.New(agent.Options{Version: Version, ServiceName: serviceName, DataDir: dataDir, ExePath: exe},
			cfg, log, collect.New(), helperhost.New(log, exe))
		if err != nil {
			log.Error("init agent", "err", err)
			return err
		}
		log.Info("NEXMIS agent starting", "version", Version, "server", cfg.ServerURL, "service", asService)
		err = a.Run(ctx)
		if err != nil {
			log.Error("agent stopped", "err", err)
		}
		return err
	})
}

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	server := fs.String("server", "", "伺服器網址，例如 https://mis.corp.local")
	token := fs.String("token", "", "註冊權杖")
	caFile := fs.String("ca", "", "企業 CA 憑證（PEM）檔案路徑")
	pin := fs.String("pin", "", "伺服器憑證 SHA-256 指紋（自簽憑證時使用）")
	insecure := fs.Bool("insecure", false, "不驗證伺服器憑證（僅限測試）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dataDir := sys.DataDir()
	if err := sys.SecureDir(dataDir); err != nil {
		return fmt.Errorf("建立資料目錄失敗（需要系統管理員權限）：%w", err)
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return err
	}
	if *server != "" {
		u := strings.TrimRight(*server, "/")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return errors.New("--server 必須以 https:// 開頭")
		}
		if u != cfg.ServerURL && cfg.ServerURL != "" {
			// Moving to another server requires a new identity.
			cfg.ResetIdentity()
		}
		cfg.ServerURL = u
	}
	if cfg.ServerURL == "" {
		return errors.New("請指定 --server")
	}
	if *token != "" {
		cfg.EnrollToken = *token
	}
	if !cfg.Enrolled() && cfg.EnrollToken == "" {
		return errors.New("此電腦尚未註冊，請指定 --token")
	}
	if *caFile != "" {
		b, err := os.ReadFile(*caFile)
		if err != nil {
			return err
		}
		cfg.TLS.CAPEM = string(b)
	}
	if *pin != "" {
		cfg.TLS.PinSHA256 = *pin
	}
	if *insecure {
		cfg.TLS.InsecureSkipVerify = true
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		fmt.Println("設定已寫入", config.Path(dataDir), "；非 Windows 平台請以 `nexmis-agent run` 執行。")
		return nil
	}
	// Copy the binary into Program Files and register the service.
	src, err := os.Executable()
	if err != nil {
		return err
	}
	installDir := sys.InstallDir()
	if err := sys.SecureDir(installDir); err != nil {
		return err
	}
	dst := filepath.Join(installDir, exeName)
	if winsvc.ServiceExists(serviceName) {
		_ = winsvc.StopService(serviceName, 30*time.Second)
	}
	if !samePath(src, dst) {
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("複製執行檔失敗：%w", err)
		}
	}
	if err := winsvc.InstallService(serviceName, displayName, description, dst); err != nil {
		return fmt.Errorf("註冊服務失敗：%w", err)
	}
	if err := winsvc.StartService(serviceName); err != nil {
		return fmt.Errorf("啟動服務失敗：%w", err)
	}
	fmt.Println("NEXMIS Agent 已安裝並啟動，伺服器：", cfg.ServerURL)
	return nil
}

func uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "一併刪除設定與身分（重新安裝時需新的註冊權杖）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if winsvc.ServiceExists(serviceName) {
		_ = winsvc.StopService(serviceName, 30*time.Second)
		if err := winsvc.RemoveService(serviceName); err != nil {
			return err
		}
	}
	if *purge {
		if err := os.RemoveAll(sys.DataDir()); err != nil {
			return err
		}
	}
	fmt.Println("NEXMIS Agent 已移除。程式檔位於", sys.InstallDir(), "，可手動刪除。")
	return nil
}

func status() error {
	cfg, err := config.Load(sys.DataDir())
	if err != nil {
		return err
	}
	fmt.Println("版本：　　", Version)
	fmt.Println("伺服器：　", nonEmpty(cfg.ServerURL, "（未設定）"))
	fmt.Println("已註冊：　", cfg.Enrolled())
	fmt.Println("裝置 ID：", nonEmpty(cfg.DeviceID, "-"))
	if runtime.GOOS == "windows" {
		fmt.Println("服務：　　", winsvc.ServiceExists(serviceName))
	}
	return nil
}

func collectDump() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := collect.New()
	out := map[string]any{}
	if v, err := c.Identity(ctx); err == nil {
		out["identity"] = v
	}
	if v, err := c.Heartbeat(ctx); err == nil {
		out["heartbeat"] = v
	}
	if v, err := c.Hardware(ctx); err == nil {
		out["hardware"] = v
	}
	if v, err := c.Health(ctx); err == nil {
		out["health"] = v
	}
	if v, err := c.Battery(ctx); err == nil {
		out["battery"] = v
	}
	if v, err := c.Software(ctx); err == nil {
		out["software_count"] = len(v)
		out["software"] = v
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func samePath(a, b string) bool {
	pa, _ := filepath.Abs(a)
	pb, _ := filepath.Abs(b)
	return strings.EqualFold(pa, pb)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}
