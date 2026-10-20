//go:build windows

package helper

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file gives the helper's prompt windows (consent, reboot, banner, chat) a
// consistent modern look without a resource compiler or cgo: Common Controls v6
// through an activation context, a DPI-scaled UI font, a white content area
// with a grey footer band, and an owner-drawn accent button for the primary
// action. Everything runs on the UI thread, so the maps below need no lock.

var (
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procCreateActCtxW            = kernel32.NewProc("CreateActCtxW")
	procActivateActCtx           = kernel32.NewProc("ActivateActCtx")
	procInitCommonControlsEx     = comctl32.NewProc("InitCommonControlsEx")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procAdjustWindowRectExForDpi = user32.NewProc("AdjustWindowRectExForDpi")
	procAdjustWindowRectEx       = user32.NewProc("AdjustWindowRectEx")
	procMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	procGetFocus                 = user32.NewProc("GetFocus")
	procGetAncestor              = user32.NewProc("GetAncestor")
	procIsDialogMessageW         = user32.NewProc("IsDialogMessageW")
	procFillRect                 = user32.NewProc("FillRect")
	procDrawTextW                = user32.NewProc("DrawTextW")
	procDrawFocusRect            = user32.NewProc("DrawFocusRect")
	procDrawIconEx               = user32.NewProc("DrawIconEx")
	procCreateFontW              = gdi32.NewProc("CreateFontW")
	procCreateSolidBrush         = gdi32.NewProc("CreateSolidBrush")
	procCreatePen                = gdi32.NewProc("CreatePen")
	procRoundRect                = gdi32.NewProc("RoundRect")
	procSetTextColor             = gdi32.NewProc("SetTextColor")
	procSetBkMode                = gdi32.NewProc("SetBkMode")
)

const (
	wmKeyDown      = 0x0100
	wmEraseBkgnd   = 0x0014
	wmDrawItem     = 0x002B
	wmCtlColorBtn  = 0x0135
	vkReturn       = 0x0D
	vkEscape       = 0x1B
	gaRoot         = 2
	bmClick        = 0x00F5
	bsOwnerDraw    = 0x000B
	ssIcon         = 0x0003
	ssNotify       = 0x0100
	ssEndEllipsis  = 0x4000
	stmSetIcon     = 0x0170
	odsSelected    = 0x0001
	odsFocus       = 0x0010
	odsDisabled    = 0x0004
	dtCenter       = 0x0001
	dtVCenter      = 0x0004
	dtSingleLine   = 0x0020
	transparentBk  = 1
	psSolid        = 0
	nullBrush      = 5
	diNormal       = 0x0003
	monitorDefPrim = 1
	actCtxInvalid  = ^uintptr(0)
	iccStandard    = 0x00004000
	fwNormal       = 400
	fwSemibold     = 600
	cleartypeQual  = 5
	defaultCharset = 1
	uiFontFace     = "Microsoft JhengHei UI"
	baseDPI        = 96
)

// Colours (COLORREF, 0x00BBGGRR).
const (
	clrWhite      = 0x00FFFFFF
	clrFooter     = 0x00F3F3F3
	clrDivider    = 0x00E5E5E5
	clrText       = 0x001A1A1A
	clrSubtle     = 0x00606060
	clrAccent     = 0x00C06700 // #0067C0
	clrAccentDown = 0x00A65400 // #0054A6
	clrWarn       = 0x001C2BC4 // #C42B1C
	clrWarnBg     = 0x00E9E7FD // #FDE7E9
	clrBannerView = 0x00C06700
	clrBannerCtrl = 0x001C2BC4
)

// commonControlsManifest opts the UI thread into Common Controls v6 so buttons
// and combo boxes use the current Windows visual style.
const commonControlsManifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
`

// actCtx mirrors ACTCTXW.
type actCtx struct {
	CbSize              uint32
	DwFlags             uint32
	LpSource            *uint16
	WProcessorArch      uint16
	WLangID             uint16
	LpAssemblyDirectory *uint16
	LpResourceName      *uint16
	LpApplicationName   *uint16
	HModule             uintptr
}

type initCommonControlsEx struct {
	DwSize uint32
	DwICC  uint32
}

// enableVisualStyles activates a Common Controls v6 context on the calling
// (UI) thread for the rest of its life. Failure leaves the classic look.
func enableVisualStyles() bool {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, "NEXMIS", "helper.manifest")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(commonControlsManifest), 0o644); err != nil {
		return false
	}
	ac := actCtx{LpSource: utf16(p)}
	ac.CbSize = uint32(unsafe.Sizeof(ac))
	h, _, _ := procCreateActCtxW.Call(uintptr(unsafe.Pointer(&ac)))
	if h == actCtxInvalid || h == 0 {
		return false
	}
	var cookie uintptr
	if r, _, _ := procActivateActCtx.Call(h, uintptr(unsafe.Pointer(&cookie))); r == 0 {
		return false
	}
	icc := initCommonControlsEx{DwICC: iccStandard}
	icc.DwSize = uint32(unsafe.Sizeof(icc))
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	return true
}

// ---- DPI ----

func windowDPI(hwnd uintptr) uint32 {
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(hwnd); d != 0 {
			return uint32(d)
		}
	}
	return baseDPI
}

// dp scales a 96-DPI length to dpi.
func dp(v int32, dpi uint32) int32 { return int32((int64(v)*int64(dpi) + baseDPI/2) / baseDPI) }

// primaryWorkArea returns the primary monitor's work area (excludes taskbar).
func primaryWorkArea() windows.Rect {
	mon, _, _ := procMonitorFromPoint.Call(0, monitorDefPrim) // POINT{0,0} packs to 0
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	if mon != 0 {
		if r, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
			return mi.RcWork
		}
	}
	return windows.Rect{Right: getSystemMetrics(smCxScreen), Bottom: getSystemMetrics(smCyScreen)}
}

// createScaledWindow creates a hidden top-level window whose client area is
// cw×ch DIPs at the primary monitor's DPI, centred in its work area. It returns
// the window and that DPI.
func createScaledWindow(class *uint16, title string, style, exStyle uint32, cw, ch int32) (uintptr, uint32) {
	wa := primaryWorkArea()
	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16(title))),
		uintptr(style),
		uintptr(wa.Left), uintptr(wa.Top), 1, 1,
		0, 0, 0, 0)
	if hwnd == 0 {
		return 0, baseDPI
	}
	dpi := windowDPI(hwnd)
	r := windows.Rect{Right: dp(cw, dpi), Bottom: dp(ch, dpi)}
	if procAdjustWindowRectExForDpi.Find() == nil {
		procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, uintptr(exStyle), uintptr(dpi))
	} else {
		procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, uintptr(exStyle))
	}
	w, h := r.Right-r.Left, r.Bottom-r.Top
	x := wa.Left + (wa.Right-wa.Left-w)/2
	y := wa.Top + (wa.Bottom-wa.Top-h)/2
	procMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0)
	return hwnd, dpi
}

// ---- fonts and brushes (cached for the helper's lifetime) ----

type fontKey struct {
	dpi    uint32
	pt     int32
	weight int32
}

var (
	fontCache  = map[fontKey]uintptr{}
	brushCache = map[uint32]uintptr{}
)

// uiFont returns the helper UI font at pt points for dpi.
func uiFont(dpi uint32, pt, weight int32) uintptr {
	k := fontKey{dpi, pt, weight}
	if f, ok := fontCache[k]; ok {
		return f
	}
	height := -int32((int64(pt)*int64(dpi) + 36) / 72)
	f, _, _ := procCreateFontW.Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		defaultCharset, 0, 0, cleartypeQual, 0, uintptr(unsafe.Pointer(utf16(uiFontFace))))
	if f == 0 {
		f = guiFont()
	}
	fontCache[k] = f
	return f
}

var penCache = map[uint32]uintptr{}

func solidPen(c uint32) uintptr {
	if p, ok := penCache[c]; ok {
		return p
	}
	p, _, _ := procCreatePen.Call(psSolid, 1, uintptr(c))
	penCache[c] = p
	return p
}

func solidBrush(c uint32) uintptr {
	if b, ok := brushCache[c]; ok {
		return b
	}
	b, _, _ := procCreateSolidBrush.Call(uintptr(c))
	brushCache[c] = b
	return b
}

// ---- per-window skin ----

// ctlSkin is how a child control is coloured.
type ctlSkin struct {
	fg, bg uint32
}

// winSkin describes a themed top-level window: a background, an optional
// footer band from footerTop down, per-control colours and accent buttons.
type winSkin struct {
	dpi       uint32
	bg        uint32
	footer    uint32
	footerTop int32 // client y in pixels; 0 = no footer
	ctl       map[uintptr]ctlSkin
	accent    map[uintptr]uint32 // owner-drawn primary buttons -> fill colour
	icon      uintptr
	iconRect  windows.Rect
	iconPlate bool
	barRect   windows.Rect
	barMax    int
	barValue  int
	barColour uint32
}

var skins = map[uintptr]*winSkin{}

func newSkin(hwnd uintptr, dpi uint32, bg uint32) *winSkin {
	s := &winSkin{dpi: dpi, bg: bg, ctl: map[uintptr]ctlSkin{}, accent: map[uintptr]uint32{}}
	skins[hwnd] = s
	return s
}

func dropSkin(hwnd uintptr) { delete(skins, hwnd) }

// label creates a STATIC text control with the given font and colours.
func (s *winSkin) label(parent uintptr, text string, x, y, w, h int32, pt, weight int32, fg, bg uint32, extra uint32) uintptr {
	c := createControl(parent, staticClassName, text, ssLeft|uint32(wsChild)|wsVisible|extra,
		dp(x, s.dpi), dp(y, s.dpi), dp(w, s.dpi), dp(h, s.dpi), 0)
	sendMessage(c, wmSetFont, uiFont(s.dpi, pt, weight), 1)
	s.ctl[c] = ctlSkin{fg: fg, bg: bg}
	return c
}

// button creates a push button. primary buttons are owner-drawn in fill.
func (s *winSkin) button(parent uintptr, text string, x, y, w, h int32, id uintptr, primary bool, fill uint32, extra uint32) uintptr {
	style := uint32(wsChild) | wsVisible | wsTabStop | extra
	if primary {
		style |= bsOwnerDraw
	} else {
		style |= bsPushButton
	}
	c := createControl(parent, buttonClassName, text, style,
		dp(x, s.dpi), dp(y, s.dpi), dp(w, s.dpi), dp(h, s.dpi), id)
	sendMessage(c, wmSetFont, uiFont(s.dpi, 10, fwNormal), 1)
	if primary {
		s.accent[c] = fill
	}
	return c
}

// setBar places a countdown bar at (x, y, w, h) DIPs, full at full. It is
// painted with the background, so it needs no control and never animates.
func (s *winSkin) setBar(x, y, w, h int32, full int, colour uint32) {
	s.barRect = windows.Rect{Left: dp(x, s.dpi), Top: dp(y, s.dpi), Right: dp(x+w, s.dpi), Bottom: dp(y+h, s.dpi)}
	s.barMax, s.barValue, s.barColour = full, full, colour
}

// updateBar sets the countdown bar's value and repaints it.
func (s *winSkin) updateBar(hwnd uintptr, value int) {
	s.barValue = value
	procInvalidateRect.Call(hwnd, uintptr(unsafe.Pointer(&s.barRect)), 1)
}

func (s *winSkin) paintBar(hdc uintptr) {
	if s.barMax <= 0 {
		return
	}
	track := s.barRect
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&track)), solidBrush(clrDivider))
	fill := track
	fill.Right = fill.Left + int32(int64(track.Right-track.Left)*int64(max(s.barValue, 0))/int64(s.barMax))
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&fill)), solidBrush(s.barColour))
}

// setIcon draws the app icon at (x, y) DIPs, size DIPs square.
func (s *winSkin) setIcon(icon uintptr, x, y, size int32) {
	s.icon = icon
	s.iconRect = windows.Rect{Left: dp(x, s.dpi), Top: dp(y, s.dpi), Right: dp(x+size, s.dpi), Bottom: dp(y+size, s.dpi)}
}

// skinProc handles painting messages for a skinned window. ok reports whether
// the message was consumed.
func skinProc(hwnd uintptr, m uint32, wp, lp uintptr) (uintptr, bool) {
	s := skins[hwnd]
	if s == nil {
		return 0, false
	}
	switch m {
	case wmEraseBkgnd:
		var rc windows.Rect
		procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		hdc := wp
		body := rc
		if s.footerTop > 0 {
			body.Bottom = s.footerTop
			foot := rc
			foot.Top = s.footerTop
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&foot)), solidBrush(s.footer))
			line := foot
			line.Bottom = line.Top + 1
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&line)), solidBrush(clrDivider))
		}
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&body)), solidBrush(s.bg))
		if s.icon != 0 && s.iconPlate {
			// White rounded plate so the (blue) mark reads on a coloured band.
			in := dp(3, s.dpi)
			r := s.iconRect
			oldPen, _, _ := procSelectObject.Call(hdc, solidPen(clrWhite))
			oldBrush, _, _ := procSelectObject.Call(hdc, solidBrush(clrWhite))
			procRoundRect.Call(hdc, uintptr(r.Left-in), uintptr(r.Top-in), uintptr(r.Right+in), uintptr(r.Bottom+in),
				uintptr(2*in), uintptr(2*in))
			procSelectObject.Call(hdc, oldBrush)
			procSelectObject.Call(hdc, oldPen)
		}
		if s.icon != 0 {
			procDrawIconEx.Call(hdc, uintptr(s.iconRect.Left), uintptr(s.iconRect.Top), s.icon,
				uintptr(s.iconRect.Right-s.iconRect.Left), uintptr(s.iconRect.Bottom-s.iconRect.Top), 0, 0, diNormal)
		}
		s.paintBar(hdc)
		return 1, true
	case wmCtlColorStatc, wmCtlColorBtn:
		cs, ok := s.ctl[lp]
		if !ok {
			// Buttons/other controls: match the area they sit on.
			cs = ctlSkin{fg: clrText, bg: s.areaColour(lp)}
		}
		procSetTextColor.Call(wp, uintptr(cs.fg))
		procSetBkMode.Call(wp, transparentBk)
		return solidBrush(cs.bg), true
	case wmDrawItem:
		// lParam is a DRAWITEMSTRUCT* owned by the system for this call.
		di := *(**drawItemStruct)(unsafe.Pointer(&lp))
		fill, ok := s.accent[di.HwndItem]
		if !ok {
			return 0, false
		}
		drawAccentButton(di, fill, s.dpi)
		return 1, true
	}
	return 0, false
}

// areaColour returns the background under a child control.
func (s *winSkin) areaColour(child uintptr) uint32 {
	if s.footerTop <= 0 {
		return s.bg
	}
	var rc windows.Rect
	procGetWindowRect.Call(child, uintptr(unsafe.Pointer(&rc)))
	pt := point{X: rc.Left, Y: rc.Top}
	procScreenToClient.Call(getParent(child), uintptr(unsafe.Pointer(&pt)))
	if pt.Y >= s.footerTop {
		return s.footer
	}
	return s.bg
}

var (
	procGetWindowRect  = user32.NewProc("GetWindowRect")
	procScreenToClient = user32.NewProc("ScreenToClient")
	procGetParent      = user32.NewProc("GetParent")
)

func getParent(h uintptr) uintptr {
	p, _, _ := procGetParent.Call(h)
	return p
}

// drawItemStruct mirrors DRAWITEMSTRUCT.
type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   uintptr
	HDC        uintptr
	RcItem     windows.Rect
	ItemData   uintptr
}

func drawAccentButton(di *drawItemStruct, fill uint32, dpi uint32) {
	hdc := di.HDC
	rc := di.RcItem
	// Clear the corners with the parent's colour first.
	parent := getParent(di.HwndItem)
	if s := skins[parent]; s != nil {
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), solidBrush(s.areaColour(di.HwndItem)))
	}
	c := fill
	if di.ItemState&odsSelected != 0 {
		c = darken(fill)
	}
	pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(c))
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	oldBrush, _, _ := procSelectObject.Call(hdc, solidBrush(c))
	radius := dp(8, dpi)
	procRoundRect.Call(hdc, uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right), uintptr(rc.Bottom), uintptr(radius), uintptr(radius))
	procSelectObject.Call(hdc, oldBrush)
	procSelectObject.Call(hdc, oldPen)
	procDeleteObject.Call(pen)

	text := getText(di.HwndItem)
	procSetBkMode.Call(hdc, transparentBk)
	procSetTextColor.Call(hdc, clrWhite)
	oldFont, _, _ := procSelectObject.Call(hdc, uiFont(dpi, 10, fwSemibold))
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(utf16(text))), ^uintptr(0),
		uintptr(unsafe.Pointer(&rc)), dtCenter|dtVCenter|dtSingleLine)
	procSelectObject.Call(hdc, oldFont)

	if di.ItemState&odsFocus != 0 {
		in := dp(3, dpi)
		f := windows.Rect{Left: rc.Left + in, Top: rc.Top + in, Right: rc.Right - in, Bottom: rc.Bottom - in}
		procDrawFocusRect.Call(hdc, uintptr(unsafe.Pointer(&f)))
	}
}

func darken(c uint32) uint32 {
	r, g, b := c&0xFF, (c>>8)&0xFF, (c>>16)&0xFF
	return (r*4/5)&0xFF | ((g*4/5)&0xFF)<<8 | ((b*4/5)&0xFF)<<16
}

// ---- keyboard ----

// dialogKeys gives the helper's prompt windows dialog-style keyboard handling:
// Esc closes (the window's own "safe" answer), Enter presses the focused
// button, and Tab moves between controls. It returns true when m was consumed.
func (u *ui) dialogKeys(m *msg) bool {
	root, _, _ := procGetAncestor.Call(m.Hwnd, gaRoot)
	if root == 0 {
		return false
	}
	isPrompt := isConsentWindow(root) || isRebootWindow(root)
	isChat := root == u.chatHwnd && u.chatHwnd != 0
	if !isPrompt && !isChat {
		return false
	}
	if m.Message == wmKeyDown {
		focus, _, _ := procGetFocus.Call()
		switch m.WParam {
		case vkEscape:
			if !isPrompt {
				return false
			}
			postMessage(root, wmClose, 0, 0)
			return true
		case vkReturn:
			if isChat {
				if focus == theChat.input {
					postMessage(root, wmCommand, idChatSend, 0)
					return true
				}
				return false
			}
			if focus != 0 && getParent(focus) == root && isButton(focus) {
				sendMessage(focus, bmClick, 0, 0)
			}
			return true
		}
	}
	if isPrompt {
		r, _, _ := procIsDialogMessageW.Call(root, uintptr(unsafe.Pointer(m)))
		return r != 0
	}
	return false
}

var procGetClassNameW = user32.NewProc("GetClassNameW")

func isButton(h uintptr) bool {
	var buf [32]uint16
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return n > 0 && strings.EqualFold(windows.UTF16ToString(buf[:n]), "Button")
}
