//go:build windows

package collect

// A small WMI client built directly on go-ole.
//
// Why not github.com/yusufpapurcu/wmi: its Query() serialises every call on
// a package-level mutex and has no way to time out, so one hung WMI provider
// (it happens, e.g. with broken storage or Defender providers) would block
// every later collection forever. Here all queries run on a dedicated,
// OS-thread-locked COM worker that reuses its SWbemLocator and per-namespace
// SWbemServices connections. A query that exceeds its deadline causes the
// worker to be abandoned (it exits once the stuck call returns) and a fresh
// worker to be started; the number of abandoned workers is capped so a
// wedged WMI service cannot pile up threads. The worker shuts down after an
// idle period so the agent does not keep COM state resident between the
// (infrequent) inventory runs.

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const (
	wmiQueryTimeout = 20 * time.Second
	wmiIdleTimeout  = 90 * time.Second
	wmiMaxAbandoned = 2

	nsCIMV2      = `root\CIMV2`
	nsWMI        = `root\WMI`
	nsStorage    = `root\Microsoft\Windows\Storage`
	nsTPM        = `root\CIMV2\Security\MicrosoftTpm`
	nsBitLocker  = `root\CIMV2\Security\MicrosoftVolumeEncryption`
	nsDefender   = `root\Microsoft\Windows\Defender`
	nsSecCenter2 = `root\SecurityCenter2`

	wbemFlagReturnImmediately  = 0x10
	wbemFlagForwardOnly        = 0x20
	wbemConnectFlagUseMaxWait  = 0x80
	wbemAuthnLevelPktPrivacy   = 6
	wbemImpersonateImpersonate = 3
)

var errWMIUnresponsive = errors.New("wmi: service unresponsive")

type wmiRow map[string]any

type wmiRequest struct {
	namespace string
	query     string
	props     []string
	done      chan wmiResult
}

type wmiResult struct {
	rows []wmiRow
	err  error
}

type wmiWorker struct {
	reqs      chan *wmiRequest
	exited    chan struct{}
	abandoned atomic.Bool
	initErr   error
}

type wmiPool struct {
	mu        sync.Mutex
	cur       *wmiWorker
	abandoned int
}

var wmiShared wmiPool

func (p *wmiPool) get() (*wmiWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil {
		return p.cur, nil
	}
	if p.abandoned >= wmiMaxAbandoned {
		return nil, errWMIUnresponsive
	}
	w := &wmiWorker{reqs: make(chan *wmiRequest), exited: make(chan struct{})}
	p.cur = w
	go w.run(p)
	return w, nil
}

func (p *wmiPool) abandon(w *wmiWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w.abandoned.Swap(true) {
		return
	}
	if p.cur == w {
		p.cur = nil
	}
	select {
	case <-w.exited:
		// Already gone; nothing to account for.
	default:
		p.abandoned++
	}
}

func (p *wmiPool) workerExited(w *wmiWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == w {
		p.cur = nil
	}
	if w.abandoned.Load() {
		p.abandoned--
		if p.abandoned < 0 {
			p.abandoned = 0
		}
	}
	close(w.exited)
}

// wmiQuery runs "SELECT <props> FROM <class> [WHERE <where>]" in namespace
// and returns one map per instance holding the requested properties
// (missing/NULL properties are absent). selectAll uses SELECT * (for
// classes where some properties only exist on newer builds).
func wmiQuery(ctx context.Context, namespace, class, where string, props []string) ([]wmiRow, error) {
	q := "SELECT " + strings.Join(props, ",") + " FROM " + class
	if where != "" {
		q += " WHERE " + where
	}
	return wmiExec(ctx, namespace, q, props)
}

func wmiQueryAll(ctx context.Context, namespace, class, where string, props []string) ([]wmiRow, error) {
	q := "SELECT * FROM " + class
	if where != "" {
		q += " WHERE " + where
	}
	return wmiExec(ctx, namespace, q, props)
}

func wmiExec(ctx context.Context, namespace, query string, props []string) ([]wmiRow, error) {
	ctx, cancel := context.WithTimeout(ctx, wmiQueryTimeout)
	defer cancel()
	req := &wmiRequest{namespace: namespace, query: query, props: props, done: make(chan wmiResult, 1)}
	for attempt := 0; attempt < 3; attempt++ {
		w, err := wmiShared.get()
		if err != nil {
			return nil, err
		}
		select {
		case w.reqs <- req:
		case <-w.exited:
			if w.initErr != nil {
				return nil, w.initErr
			}
			continue // idled out between get() and send; retry
		case <-ctx.Done():
			return nil, fmt.Errorf("wmi %q: %w", query, ctx.Err())
		}
		select {
		case r := <-req.done:
			if r.err != nil {
				return r.rows, fmt.Errorf("wmi %s %q: %w", namespace, query, r.err)
			}
			return r.rows, nil
		case <-ctx.Done():
			wmiShared.abandon(w)
			return nil, fmt.Errorf("wmi %q: %w", query, ctx.Err())
		}
	}
	return nil, errWMIUnresponsive
}

func (w *wmiWorker) run(p *wmiPool) {
	// The goroutine stays locked to its thread for its whole life; when it
	// returns, the runtime terminates the thread, which also discards any
	// COM state we might have failed to clean up.
	runtime.LockOSThread()
	defer p.workerExited(w)

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oe *ole.OleError
		if !errors.As(err, &oe) || (oe.Code() != ole.S_OK && oe.Code() != 1 /* S_FALSE */) {
			w.initErr = fmt.Errorf("CoInitializeEx: %w", err)
			return
		}
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil || unknown == nil {
		w.initErr = fmt.Errorf("create SWbemLocator: %v", err)
		return
	}
	defer unknown.Release()
	locator, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		w.initErr = fmt.Errorf("SWbemLocator IDispatch: %w", err)
		return
	}
	defer locator.Release()
	setWbemSecurity(locator)

	services := map[string]*ole.IDispatch{}
	defer func() {
		for _, s := range services {
			s.Release()
		}
	}()

	idle := time.NewTimer(wmiIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case req := <-w.reqs:
			rows, err := w.execute(locator, services, req)
			req.done <- wmiResult{rows: rows, err: err}
			if w.abandoned.Load() {
				return
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(wmiIdleTimeout)
		case <-idle.C:
			return
		}
	}
}

func setWbemSecurity(obj *ole.IDispatch) {
	secRaw, err := oleutil.GetProperty(obj, "Security_")
	if err != nil {
		return
	}
	defer secRaw.Clear()
	sec := secRaw.ToIDispatch()
	if sec == nil {
		return
	}
	// Packet privacy is required by root\CIMV2\Security\* (BitLocker, TPM).
	if v, err := oleutil.PutProperty(sec, "AuthenticationLevel", int32(wbemAuthnLevelPktPrivacy)); err == nil {
		v.Clear()
	}
	if v, err := oleutil.PutProperty(sec, "ImpersonationLevel", int32(wbemImpersonateImpersonate)); err == nil {
		v.Clear()
	}
}

func (w *wmiWorker) service(locator *ole.IDispatch, services map[string]*ole.IDispatch, ns string) (*ole.IDispatch, error) {
	key := strings.ToLower(ns)
	if s, ok := services[key]; ok {
		return s, nil
	}
	// ConnectServer(strServer, strNamespace, strUser, strPassword, strLocale,
	//               strAuthority, iSecurityFlags)
	raw, err := oleutil.CallMethod(locator, "ConnectServer", ".", ns, "", "", "", "", int32(wbemConnectFlagUseMaxWait))
	if err != nil {
		return nil, fmt.Errorf("ConnectServer %s: %w", ns, err)
	}
	svc := raw.ToIDispatch()
	if svc == nil {
		raw.Clear()
		return nil, fmt.Errorf("ConnectServer %s: nil service", ns)
	}
	// Keep our own reference; the VARIANT owns one which we hand over.
	setWbemSecurity(svc)
	services[key] = svc
	return svc, nil
}

func (w *wmiWorker) execute(locator *ole.IDispatch, services map[string]*ole.IDispatch, req *wmiRequest) (rows []wmiRow, err error) {
	defer func() {
		// A malformed VARIANT must not take the agent down.
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	svc, err := w.service(locator, services, req.namespace)
	if err != nil {
		return nil, err
	}
	resRaw, err := oleutil.CallMethod(svc, "ExecQuery", req.query, "WQL", int32(wbemFlagReturnImmediately|wbemFlagForwardOnly))
	if err != nil {
		// A broken cached connection (e.g. winmgmt restarted) is dropped so
		// the next query reconnects.
		delete(services, strings.ToLower(req.namespace))
		svc.Release()
		return nil, err
	}
	defer resRaw.Clear()
	result := resRaw.ToIDispatch()
	if result == nil {
		return nil, errors.New("ExecQuery returned nil")
	}
	enumProp, err := result.GetProperty("_NewEnum")
	if err != nil {
		return nil, err
	}
	defer enumProp.Clear()
	enumUnk := enumProp.ToIUnknown()
	if enumUnk == nil {
		return nil, errors.New("_NewEnum returned nil")
	}
	enum, err := enumUnk.IEnumVARIANT(ole.IID_IEnumVariant)
	if err != nil {
		return nil, err
	}
	if enum == nil {
		return nil, errors.New("IEnumVARIANT is nil")
	}
	defer enum.Release()

	for {
		itemRaw, n, nerr := enum.Next(1)
		if n == 0 {
			// With forward-only enumerators, provider failures surface
			// here as an error with no item.
			if nerr != nil {
				var oe *ole.OleError
				if errors.As(nerr, &oe) && oe.Code() == 1 { // S_FALSE: end
					break
				}
				return rows, nerr
			}
			break
		}
		item := itemRaw.ToIDispatch()
		if item == nil {
			itemRaw.Clear()
			continue
		}
		row := make(wmiRow, len(req.props))
		for _, p := range req.props {
			v, perr := oleutil.GetProperty(item, p)
			if perr != nil {
				continue
			}
			if val := variantValue(v); val != nil {
				row[p] = val
			}
			v.Clear()
		}
		itemRaw.Clear()
		rows = append(rows, row)
	}
	return rows, nil
}

// variantValue converts a property VARIANT into a plain Go value. Arrays
// become []any. Object-valued properties are ignored.
func variantValue(v *ole.VARIANT) any {
	if v == nil {
		return nil
	}
	if v.VT == ole.VT_NULL || v.VT == ole.VT_EMPTY {
		return nil
	}
	if v.VT&ole.VT_ARRAY != 0 {
		elem := v.VT &^ ole.VT_ARRAY
		// go-ole reads VT_BOOL array elements into a 1-byte bool (VARIANT_BOOL
		// is 2 bytes) - never convert those.
		if elem == ole.VT_BOOL || elem == ole.VT_DISPATCH || elem == ole.VT_UNKNOWN {
			return nil
		}
		sa := v.ToArray()
		if sa == nil {
			return nil
		}
		vals := sa.ToValueArray()
		out := make([]any, 0, len(vals))
		for _, e := range vals {
			switch e.(type) {
			case *ole.IDispatch, *ole.IUnknown:
				continue
			}
			out = append(out, e)
		}
		return out
	}
	switch v.VT {
	case ole.VT_DISPATCH, ole.VT_UNKNOWN:
		return nil
	}
	return v.Value()
}
