package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
	"github.com/tglobalcorp-mis/nexmis/internal/discovery"
	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

const maxOutput = 1 << 20

func failed(format string, a ...any) protocol.CommandResult {
	return protocol.CommandResult{Status: protocol.StatusFailed, ExitCode: -1, Stderr: fmt.Sprintf(format, a...)}
}

func ok(stdout string) protocol.CommandResult {
	return protocol.CommandResult{Status: protocol.StatusSucceeded, Stdout: stdout}
}

func (a *Agent) execute(ctx context.Context, c protocol.Command) protocol.CommandResult {
	timeout := time.Duration(c.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	switch c.Kind {
	case protocol.CmdPowerShell, protocol.CmdCmd:
		var args protocol.ScriptArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		if args.RunAs == "user" {
			return a.runAsUser(ctx, c.Kind, args, timeout)
		}
		return runScript(ctx, c.Kind, args.Script, args.Env, timeout)

	case protocol.CmdReboot, protocol.CmdShutdown:
		var args protocol.RebootArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		return a.prepareReboot(ctx, c, args)

	case protocol.CmdRefresh:
		a.invMu.Lock()
		a.invHash = map[string]string{}
		a.invMu.Unlock()
		a.requestRefresh("all")
		return ok("已重新收集盤點、健康與電池資料")

	case protocol.CmdNotify:
		var args protocol.NotifyArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		if err := a.host.Send(helperipc.TNotify, c.CommandID, helperipc.Notify{Title: args.Title, Message: args.Message, Level: args.Level}); err != nil {
			return failed("目前沒有使用者登入，無法顯示通知")
		}
		return ok("已顯示通知")

	case protocol.CmdLock:
		if err := a.host.Send(helperipc.TLock, c.CommandID, nil); err != nil {
			return failed("目前沒有使用者登入")
		}
		return ok("已鎖定工作站")

	case protocol.CmdLogoff:
		if err := sys.Logoff(); err != nil {
			return failed("登出失敗：%v", err)
		}
		return ok("已登出使用者")

	case protocol.CmdServiceRestart:
		var args protocol.ServiceArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		if strings.EqualFold(args.Name, a.opt.ServiceName) {
			return failed("不可透過此指令重新啟動 NEXMIS Agent 本身")
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := sys.RestartService(cctx, args.Name); err != nil {
			return failed("重新啟動服務失敗：%v", err)
		}
		return ok("已重新啟動服務 " + args.Name)

	case protocol.CmdProcessKill:
		var args protocol.ProcessArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		n, err := sys.KillProcess(args.PID, args.Name)
		if err != nil {
			return failed("終止處理程序失敗：%v", err)
		}
		return ok(fmt.Sprintf("已終止 %d 個處理程序", n))

	case protocol.CmdInstall:
		var args protocol.InstallArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		return a.install(ctx, args, timeout)

	case protocol.CmdUninstall:
		var args protocol.UninstallArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		cmd, err := sys.UninstallCommand(ctx, args.UninstallID, args.Arguments)
		if err != nil {
			return failed("找不到解除安裝程式：%v", err)
		}
		res := runCmd(ctx, cmd, func() {}, timeout)
		if res.Status == protocol.StatusFailed && slices.Contains([]int{3010, 1641}, res.ExitCode) {
			res.Status = protocol.StatusSucceeded
			res.Stdout += "\n（需要重新開機以完成解除安裝）"
		}
		if res.Status == protocol.StatusSucceeded {
			a.requestRefresh("software")
		}
		return res

	case protocol.CmdAgentUpdate:
		var args protocol.AgentUpdateArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		if err := a.selfUpdate(ctx, args.Version, protocol.AgentBuild{URL: args.DownloadURL, SHA256: args.SHA256}); err != nil {
			return failed("更新失敗：%v", err)
		}
		return ok("更新中，Agent 即將重新啟動")

	case protocol.CmdScan:
		var args protocol.ScanArgs
		if err := json.Unmarshal(c.Args, &args); err != nil {
			return failed("參數錯誤：%v", err)
		}
		addrs, err := discovery.ExpandCIDRs(args.CIDRs)
		if err != nil {
			return failed("%v", err)
		}
		sctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		hosts, err := discovery.Scan(sctx, addrs, discovery.Options{Ports: args.Ports})
		if err != nil {
			return failed("掃描失敗：%v", err)
		}
		data, _ := json.Marshal(hosts)
		r := ok(fmt.Sprintf("掃描 %d 個位址，發現 %d 台主機", len(addrs), len(hosts)))
		r.Data = data
		return r
	}
	return protocol.CommandResult{Status: protocol.StatusRejected, ExitCode: -1, Stderr: "此 Agent 版本不支援指令類型 " + c.Kind}
}

// ---- process execution ----

// cappedBuffer keeps at most max bytes.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(b.buf.String(), "�")
}

func runScript(ctx context.Context, shell, script string, env map[string]string, timeout time.Duration) protocol.CommandResult {
	cmd, cleanup, err := sys.ScriptCommand(ctx, shell, script, env)
	if err != nil {
		return failed("無法建立執行環境：%v", err)
	}
	return runCmd(ctx, cmd, cleanup, timeout)
}

// runCmd runs cmd with a timeout, killing the whole process tree on expiry.
func runCmd(ctx context.Context, cmd *exec.Cmd, cleanup func(), timeout time.Duration) protocol.CommandResult {
	defer cleanup()
	stdout := &cappedBuffer{max: maxOutput}
	stderr := &cappedBuffer{max: maxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	kill, err := sys.StartTracked(cmd)
	if err != nil {
		return failed("無法啟動：%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	status := protocol.StatusSucceeded
	select {
	case waitErr = <-done:
	case <-timer.C:
		kill()
		waitErr = <-done
		status = protocol.StatusTimeout
	case <-ctx.Done():
		kill()
		waitErr = <-done
		status = protocol.StatusCanceled
	}
	kill() // release the job object
	res := protocol.CommandResult{
		Status: status, Stdout: stdout.String(), Stderr: stderr.String(),
		Truncated: stdout.truncated || stderr.truncated, StartedAt: started, FinishedAt: time.Now(),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if status == protocol.StatusSucceeded && (waitErr != nil || res.ExitCode != 0) {
		res.Status = protocol.StatusFailed
		if res.Stderr == "" && waitErr != nil {
			res.Stderr = waitErr.Error()
		}
	}
	if status == protocol.StatusTimeout {
		res.Stderr = strings.TrimSpace(res.Stderr + fmt.Sprintf("\n執行超過 %s，已終止", timeout))
	}
	return res
}

// runAsUser runs a script in the logged-in user's session via the helper.
func (a *Agent) runAsUser(ctx context.Context, kind string, args protocol.ScriptArgs, timeout time.Duration) protocol.CommandResult {
	if !a.host.Online() {
		return failed("目前沒有使用者登入，無法以使用者身分執行")
	}
	id := uuid.NewString()
	raw, err := a.helperRoundTrip(ctx, helperipc.TRunScript, id, helperipc.RunScript{
		ID: id, Shell: kind, Script: args.Script, TimeoutSec: int(timeout.Seconds()), Env: args.Env,
	}, timeout+30*time.Second)
	if err != nil {
		return failed("使用者端執行失敗：%v", err)
	}
	var rr helperipc.RunResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		return failed("回應格式錯誤")
	}
	res := protocol.CommandResult{Status: protocol.StatusSucceeded, ExitCode: rr.ExitCode, Stdout: rr.Stdout,
		Stderr: rr.Stderr, Truncated: rr.Truncated}
	if rr.Error != "" || rr.ExitCode != 0 {
		res.Status = protocol.StatusFailed
		if rr.Error != "" {
			res.Stderr = strings.TrimSpace(res.Stderr + "\n" + rr.Error)
		}
	}
	return res
}

// helperRoundTrip sends a request to the helper and waits for the reply
// carrying the same id.
func (a *Agent) helperRoundTrip(ctx context.Context, t, id string, payload any, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	a.pendingMu.Lock()
	a.pendingRun[id] = ch
	a.pendingMu.Unlock()
	defer func() {
		a.pendingMu.Lock()
		delete(a.pendingRun, id)
		a.pendingMu.Unlock()
	}()
	if err := a.host.Send(t, id, payload); err != nil {
		return nil, err
	}
	select {
	case raw := <-ch:
		return raw, nil
	case <-time.After(timeout):
		return nil, errors.New("使用者端沒有回應")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ---- reboot ----

// prepareReboot negotiates with the user (countdown and deferrals) and
// returns success when it is time to restart. The actual restart happens in
// finishPowerAction after the result has been sent.
func (a *Agent) prepareReboot(ctx context.Context, c protocol.Command, args protocol.RebootArgs) protocol.CommandResult {
	verb := "重新開機"
	if args.Shutdown {
		verb = "關機"
	}
	if args.Force || args.CountdownSec == 0 || !a.host.Online() {
		return ok(fmt.Sprintf("立即%s", verb))
	}
	cfg := a.config()
	defersLeft := 0
	if args.AllowDefer {
		defersLeft = cfg.RebootMaxDefer
	}
	deferred := 0
	for {
		id := uuid.NewString()
		raw, err := a.helperRoundTrip(ctx, helperipc.TRebootPrompt, id, helperipc.RebootPrompt{
			ID: id, CountdownSec: args.CountdownSec, Message: args.Message, AllowDefer: defersLeft > 0,
			DefersLeft: defersLeft, Shutdown: args.Shutdown, Requester: c.IssuedBy,
		}, time.Duration(args.CountdownSec+30)*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return protocol.CommandResult{Status: protocol.StatusCanceled, Stderr: "Agent 停止"}
			}
			// Helper vanished (user logged off): proceed.
			return ok(fmt.Sprintf("使用者端無回應，%s", verb))
		}
		var resp helperipc.RebootResponse
		_ = json.Unmarshal(raw, &resp)
		if resp.Action != helperipc.RebootDefer || defersLeft <= 0 {
			msg := fmt.Sprintf("使用者同意立即%s", verb)
			if resp.Action == helperipc.RebootTimeout {
				msg = fmt.Sprintf("倒數結束，%s", verb)
			}
			if deferred > 0 {
				msg += fmt.Sprintf("（先前延後 %d 次）", deferred)
			}
			return ok(msg)
		}
		defersLeft--
		deferred++
		mins := max(5, min(resp.DeferMinutes, protocol.MaxRebootDeferMinutes))
		a.log.Info("user deferred reboot", "minutes", mins, "defers_left", defersLeft)
		select {
		case <-ctx.Done():
			return protocol.CommandResult{Status: protocol.StatusCanceled, Stderr: "Agent 停止"}
		case <-time.After(time.Duration(mins) * time.Minute):
		}
	}
}

func (a *Agent) finishPowerAction(c protocol.Command) {
	var args protocol.RebootArgs
	_ = json.Unmarshal(c.Args, &args)
	// Give the result a moment to reach the server; it is also persisted.
	for i := 0; i < 10; i++ {
		names, _ := a.outbox.Pending()
		if len(names) == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	reason := "NEXMIS: " + c.IssuedBy
	if args.Message != "" {
		reason += " - " + args.Message
	}
	if err := sys.Reboot(args.Shutdown, true, reason); err != nil {
		a.log.Error("reboot failed", "err", err)
	}
}

// ---- software deployment ----

func (a *Agent) download(ctx context.Context, url, dest, wantSHA string, size int64) error {
	if fileSHA(dest) == strings.ToLower(wantSHA) {
		return nil // cached from an earlier attempt
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	part := dest + ".part"
	var offset int64
	if st, err := os.Stat(part); err == nil && (size == 0 || st.Size() < size) {
		offset = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.absURL(url), nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("下載失敗：%s", resp.Status)
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("下載中斷：%w", err)
	}
	if got := fileSHA(part); got != strings.ToLower(wantSHA) {
		os.Remove(part)
		return fmt.Errorf("檔案雜湊不符（預期 %s，實際 %s），已捨棄", wantSHA, got)
	}
	return os.Rename(part, dest)
}

func fileSHA(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Agent) absURL(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return a.cfg.HTTPBase() + u
}

func (a *Agent) install(ctx context.Context, args protocol.InstallArgs, timeout time.Duration) protocol.CommandResult {
	if args.SHA256 == "" || args.DownloadURL == "" {
		return failed("部署參數不完整")
	}
	dir := filepath.Join(a.opt.DataDir, "packages", safeName(args.SHA256[:min(16, len(args.SHA256))]))
	dest := filepath.Join(dir, filepath.Base(args.FileName))
	// The command timeout covers download and install together, matching
	// the deadline the server enforces from the agent's ack.
	deadline := time.Now().Add(timeout)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	err := a.download(dctx, args.DownloadURL, dest, args.SHA256, args.Size)
	cancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return protocol.CommandResult{Status: protocol.StatusTimeout, ExitCode: -1, Stderr: "下載套件逾時"}
		}
		return failed("%v", err)
	}
	cmd, err := sys.InstallCommand(ctx, dest, args.Type, args.Arguments)
	if err != nil {
		return failed("無法建立安裝命令：%v", err)
	}
	res := runCmd(ctx, cmd, func() {}, time.Until(deadline))
	codes := args.SuccessCodes
	if len(codes) == 0 {
		codes = []int{0, 3010, 1641}
	}
	if res.Status != protocol.StatusTimeout && res.Status != protocol.StatusCanceled {
		if slices.Contains(codes, res.ExitCode) {
			res.Status = protocol.StatusSucceeded
			if res.ExitCode == 3010 || res.ExitCode == 1641 {
				res.Stdout = strings.TrimSpace(res.Stdout + "\n安裝完成，需要重新開機才會生效")
			}
		} else {
			res.Status = protocol.StatusFailed
			if res.Stderr == "" {
				res.Stderr = fmt.Sprintf("安裝程式結束碼 %d（%s）", res.ExitCode, describeExitCode(res.ExitCode))
			}
		}
	}
	if res.Status == protocol.StatusSucceeded {
		_ = os.RemoveAll(dir)
		a.requestRefresh("software")
	}
	return res
}

// describeExitCode explains common Windows Installer exit codes.
func describeExitCode(c int) string {
	switch c {
	case 1602:
		return "使用者取消安裝"
	case 1603:
		return "安裝時發生嚴重錯誤"
	case 1618:
		return "另一個安裝正在進行中"
	case 1619:
		return "無法開啟安裝套件"
	case 1620:
		return "安裝套件無效"
	case 1625:
		return "系統原則禁止此安裝"
	case 1633:
		return "此平台不支援此套件"
	case 1638:
		return "已安裝其他版本"
	}
	return "未知錯誤"
}
