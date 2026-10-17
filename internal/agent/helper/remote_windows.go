//go:build windows

package helper

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/coder/websocket"
	"golang.org/x/sys/windows"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/tlsconf"
)

// Compile-time layout checks: INPUT is 40 bytes on 64-bit (amd64 and arm64),
// and BITMAPINFOHEADER is 40 bytes. A wrong size makes one array length
// negative and fails the build.
var (
	_ [unsafe.Sizeof(mouseInput{}) - 40]byte
	_ [40 - unsafe.Sizeof(mouseInput{})]byte
	_ [unsafe.Sizeof(keybdInput{}) - 40]byte
	_ [40 - unsafe.Sizeof(keybdInput{})]byte
	_ [unsafe.Sizeof(bitmapInfoHeader{}) - 40]byte
	_ [40 - unsafe.Sizeof(bitmapInfoHeader{})]byte
)

const (
	tileSize       = 64
	maxUnacked     = 2
	minFrameGap    = 66 * time.Millisecond // ~15 fps ceiling
	idleCheckGap   = 200 * time.Millisecond
	activeCheckGap = 16 * time.Millisecond
)

// remoteSession streams one monitor's screen to the viewer and (in control
// mode) injects input it receives back.
type remoteSession struct {
	log   *slog.Logger
	conn  *websocket.Conn
	start helperipc.RemoteStart

	quality    atomic.Int32
	monitorIdx atomic.Int32
	refresh    atomic.Bool
	acked      atomic.Uint32
	seq        atomic.Uint32

	monitors []image.Rectangle
	virt     image.Rectangle
	prev     *frameBuf
	lastW    int
	lastH    int
	lastMon  int

	writeMu sync.Mutex
	cadFn   func()
}

// runRemote connects to the relay and streams until ctx ends or the socket
// closes. It returns the reason the session ended ("user", "error", "mis").
func runRemote(ctx context.Context, log *slog.Logger, start helperipc.RemoteStart, cad func()) (string, string) {
	tlsCfg, err := tlsconf.Config(start.TLS)
	if err != nil {
		return "error", "tls config: " + err.Error()
	}
	hc, err := tlsconf.HTTPClient(start.TLS, 0)
	if err != nil {
		return "error", err.Error()
	}
	_ = tlsCfg

	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, start.URL, &websocket.DialOptions{
		HTTPClient: hc,
		HTTPHeader: map[string][]string{"Authorization": {"Bearer " + start.Token}},
	})
	dcancel()
	if err != nil {
		return "error", "dial relay: " + err.Error()
	}
	conn.SetReadLimit(1 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")

	s := &remoteSession{log: log, conn: conn, start: start, cadFn: cad}
	s.quality.Store(defaultQuality)
	s.acked.Store(0)
	s.monitors = enumerateMonitors()
	s.virt = virtualDesktopRect()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Control reader.
	ctrlErr := make(chan error, 1)
	go func() { ctrlErr <- s.readControl(ctx) }()

	// Status: active.
	s.writeText(`{"t":"status","status":"active"}`)

	by, reason := s.streamLoop(ctx)
	cancel()
	select {
	case <-ctrlErr:
	case <-time.After(200 * time.Millisecond):
	}
	return by, reason
}

// streamLoop captures and sends frames with simple ack-based flow control and
// adaptive timing. It returns ("user"/"error"/"mis", reason).
func (s *remoteSession) streamLoop(ctx context.Context) (string, string) {
	defer recoverPanic(s.log, "streamLoop")
	var lastFrame time.Time
	for {
		if ctx.Err() != nil {
			return "mis", ""
		}
		// Flow control: no more than maxUnacked frames outstanding.
		if s.seq.Load()-s.acked.Load() >= maxUnacked {
			if !sleepCtxW(ctx, activeCheckGap) {
				return "mis", ""
			}
			continue
		}
		if gap := time.Since(lastFrame); gap < minFrameGap {
			if !sleepCtxW(ctx, minFrameGap-gap) {
				return "mis", ""
			}
		}

		mon := s.currentMonitor()
		cur, err := captureMonitor(mon)
		if err != nil {
			cur = solidFrame(mon.Dx(), mon.Dy())
		}

		full := s.refresh.Swap(false) ||
			cur.width != s.lastW || cur.height != s.lastH || s.lastMon != int(s.monitorIdx.Load())
		if full {
			s.sendScreenInfo(cur.width, cur.height)
			s.prev = nil
			s.lastW, s.lastH = cur.width, cur.height
			s.lastMon = int(s.monitorIdx.Load())
		}

		rects := mergeRects(changedTiles(s.prev, cur, tileSize), tileSize)
		if len(rects) == 0 && !full {
			// Nothing changed: idle cadence.
			if !sleepCtxW(ctx, idleCheckGap) {
				return "mis", ""
			}
			continue
		}
		q := int(s.quality.Load())
		for _, r := range rects {
			jp := encodeTileJPEG(cur, r, q)
			if jp == nil {
				continue
			}
			if err := s.writeBinary(ctx, encodeBlock(uint16(r.Min.X), uint16(r.Min.Y),
				uint16(r.Dx()), uint16(r.Dy()), jp)); err != nil {
				return "error", err.Error()
			}
		}
		seq := s.seq.Add(1)
		if err := s.writeBinary(ctx, encodeFrameEnd(seq)); err != nil {
			return "error", err.Error()
		}
		s.sendCursor(ctx, mon)
		s.prev = cur
		lastFrame = time.Now()
	}
}

func (s *remoteSession) currentMonitor() image.Rectangle {
	idx := int(s.monitorIdx.Load())
	if idx < 0 || idx >= len(s.monitors) {
		idx = 0
	}
	if len(s.monitors) == 0 {
		return virtualDesktopRect()
	}
	return s.monitors[idx]
}

func (s *remoteSession) sendScreenInfo(w, h int) {
	idx := int(s.monitorIdx.Load())
	_ = s.writeBinaryBest(encodeScreenInfo(w, h, idx, len(s.monitors)))
}

func (s *remoteSession) sendCursor(ctx context.Context, mon image.Rectangle) {
	var pt point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return
	}
	x := int(pt.X) - mon.Min.X
	y := int(pt.Y) - mon.Min.Y
	if x < 0 || y < 0 || x > mon.Dx() || y > mon.Dy() {
		return
	}
	_ = s.writeBinary(ctx, encodeCursor(uint16(x), uint16(y)))
}

func (s *remoteSession) writeBinary(ctx context.Context, b []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.conn.Write(wctx, websocket.MessageBinary, b)
}

func (s *remoteSession) writeBinaryBest(b []byte) error {
	return s.writeBinary(context.Background(), b)
}

func (s *remoteSession) writeText(str string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.conn.Write(ctx, websocket.MessageText, []byte(str))
}

// readControl processes viewer control messages until the socket closes.
func (s *remoteSession) readControl(ctx context.Context) error {
	defer recoverPanic(s.log, "readControl")
	control := s.start.Mode == "control"
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		m, err := parseCtrl(data)
		if err != nil {
			continue
		}
		switch m.T {
		case ctrlAck:
			s.acked.Store(m.Seq)
		case ctrlRefresh:
			s.refresh.Store(true)
		case ctrlQuality:
			s.quality.Store(int32(clampQuality(m.Q)))
		case ctrlMonitor:
			if m.I >= 0 && m.I < len(s.monitors) {
				s.monitorIdx.Store(int32(m.I))
			}
		case ctrlMouse:
			if control {
				s.injectMouse(m)
			}
		case ctrlKey:
			if control {
				s.injectKey(m)
			}
		case ctrlText:
			if control {
				s.injectText(m.S)
			}
		case ctrlCAD:
			if control && s.cadFn != nil {
				s.cadFn()
			}
		}
	}
}

// ---- input injection ----

func (s *remoteSession) injectMouse(m ctrlMsg) {
	mon := s.currentMonitor()
	ax, ay := toVirtualAbsolute(m.X, m.Y, mon, s.virt)
	flags := uint32(mouseEventFAbsolute | mouseEventFVirtualDsk)
	var data uint32
	switch m.A {
	case "move":
		flags |= mouseEventFMove
	case "down":
		switch m.B {
		case 1:
			flags |= mouseEventFMiddleDown
		case 2:
			flags |= mouseEventFRightDown
		default:
			flags |= mouseEventFLeftDown
		}
	case "up":
		switch m.B {
		case 1:
			flags |= mouseEventFMiddleUp
		case 2:
			flags |= mouseEventFRightUp
		default:
			flags |= mouseEventFLeftUp
		}
	case "wheel":
		flags |= mouseEventFWheel
		// d follows WHEEL_DELTA: positive scrolls up (the viewer sends -deltaY).
		data = uint32(int32(m.D))
	default:
		return
	}
	in := mouseInput{
		Type:      inputMouse,
		Dx:        ax,
		Dy:        ay,
		MouseData: data,
		DwFlags:   flags,
	}
	sendOneInput(unsafe.Pointer(&in))
}

func (s *remoteSession) injectKey(m ctrlMsg) {
	sc, ext, ok := lookupScanCode(m.Code)
	if !ok {
		return
	}
	flags := uint32(keyEventFScancode)
	if ext {
		flags |= keyEventFExtendedKey
	}
	if m.A == "up" {
		flags |= keyEventFKeyUp
	}
	in := keybdInput{Type: inputKeyboard, WScan: sc, DwFlags: flags}
	sendOneInput(unsafe.Pointer(&in))
}

func (s *remoteSession) injectText(text string) {
	for _, r := range text {
		for _, u := range utf16Runes(r) {
			down := keybdInput{Type: inputKeyboard, WScan: u, DwFlags: keyEventFUnicode}
			sendOneInput(unsafe.Pointer(&down))
			up := keybdInput{Type: inputKeyboard, WScan: u, DwFlags: keyEventFUnicode | keyEventFKeyUp}
			sendOneInput(unsafe.Pointer(&up))
		}
	}
}

// utf16Runes splits a rune into one or two UTF-16 code units.
func utf16Runes(r rune) []uint16 {
	if r <= 0xFFFF {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
}

func sendOneInput(p unsafe.Pointer) {
	const inputSize = 40 // sizeof(INPUT) on 64-bit
	procSendInput.Call(1, uintptr(p), inputSize)
}

// ---- capture ----

// Monitor enumeration accumulates into a package variable under a lock, so the
// callback never converts the lparam uintptr back into a Go pointer.
var (
	enumMu         sync.Mutex
	enumAcc        []image.Rectangle
	enumMonitorsCB = windows.NewCallback(func(hMon, hdc, lprc, lparam uintptr) uintptr {
		var mi monitorInfo
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi))); r != 0 {
			rc := mi.RcMonitor
			enumAcc = append(enumAcc, image.Rect(int(rc.Left), int(rc.Top), int(rc.Right), int(rc.Bottom)))
		}
		return 1 // continue enumeration
	})
)

// enumerateMonitors returns each monitor's rectangle in virtual-desktop
// coordinates, primary monitor first.
func enumerateMonitors() []image.Rectangle {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumAcc = nil
	procEnumDisplayMonitors.Call(0, 0, enumMonitorsCB, 0)
	mons := enumAcc
	enumAcc = nil
	// Put the primary (contains 0,0) first.
	for i, m := range mons {
		if m.Min.X == 0 && m.Min.Y == 0 && i != 0 {
			mons[0], mons[i] = mons[i], mons[0]
			break
		}
	}
	if len(mons) == 0 {
		mons = append(mons, virtualDesktopRect())
	}
	return mons
}

func virtualDesktopRect() image.Rectangle {
	x := int(getSystemMetrics(smXVirtualScreen))
	y := int(getSystemMetrics(smYVirtualScreen))
	w := int(getSystemMetrics(smCxVirtualScr))
	h := int(getSystemMetrics(smCyVirtualScr))
	if w == 0 || h == 0 {
		w = int(getSystemMetrics(smCxScreen))
		h = int(getSystemMetrics(smCyScreen))
	}
	return image.Rect(x, y, x+w, y+h)
}

// captureMonitor grabs one monitor's pixels via GDI into a top-down BGRA buffer.
func captureMonitor(mon image.Rectangle) (*frameBuf, error) {
	w, h := mon.Dx(), mon.Dy()
	if w <= 0 || h <= 0 {
		return nil, windows.ERROR_INVALID_PARAMETER
	}
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, windows.ERROR_INVALID_HANDLE
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, windows.ERROR_NOT_ENOUGH_MEMORY
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, windows.ERROR_NOT_ENOUGH_MEMORY
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	if r, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(mon.Min.X), uintptr(mon.Min.Y), srccopy|captureBlt); r == 0 {
		return nil, windows.GetLastError()
	}

	buf := &frameBuf{pix: make([]byte, w*h*4), stride: w * 4, width: w, height: h}
	var bi bitmapInfo
	bi.Header = bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(w),
		Height:      int32(-h), // negative = top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	if r, _, _ := procGetDIBits.Call(memDC, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf.pix[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors); r == 0 {
		return nil, windows.GetLastError()
	}
	return buf, nil
}

func solidFrame(w, h int) *frameBuf {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	return &frameBuf{pix: make([]byte, w*h*4), stride: w * 4, width: w, height: h}
}

// encodeTileJPEG extracts rect r from a BGRA frame and JPEG-encodes it.
func encodeTileJPEG(buf *frameBuf, r image.Rectangle, quality int) []byte {
	w, h := r.Dx(), r.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcOff := (r.Min.Y+y)*buf.stride + r.Min.X*4
		dstOff := y * img.Stride
		for x := 0; x < w; x++ {
			so := srcOff + x*4
			do := dstOff + x*4
			if so+3 >= len(buf.pix) {
				break
			}
			// BGRA -> RGBA
			img.Pix[do] = buf.pix[so+2]
			img.Pix[do+1] = buf.pix[so+1]
			img.Pix[do+2] = buf.pix[so]
			img.Pix[do+3] = 0xFF
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil
	}
	return b.Bytes()
}

func sleepCtxW(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
