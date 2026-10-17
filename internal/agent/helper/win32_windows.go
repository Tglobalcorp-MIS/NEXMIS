//go:build windows

package helper

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// This file is the Win32 foundation for the helper's UI and capture code: DLL
// procedures, struct layouts (checked for 64-bit/ARM64 alignment) and small
// wrappers. Everything here is loaded lazily through x/sys/windows so the build
// stays cgo-free and cross-compiles for amd64 and arm64.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW        = user32.NewProc("RegisterClassExW")
	procCreateWindowExW         = user32.NewProc("CreateWindowExW")
	procDestroyWindow           = user32.NewProc("DestroyWindow")
	procDefWindowProcW          = user32.NewProc("DefWindowProcW")
	procGetMessageW             = user32.NewProc("GetMessageW")
	procPeekMessageW            = user32.NewProc("PeekMessageW")
	procTranslateMessage        = user32.NewProc("TranslateMessage")
	procDispatchMessageW        = user32.NewProc("DispatchMessageW")
	procPostMessageW            = user32.NewProc("PostMessageW")
	procPostQuitMessage         = user32.NewProc("PostQuitMessage")
	procSendMessageW            = user32.NewProc("SendMessageW")
	procPostThreadMessageW      = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId      = kernel32.NewProc("GetCurrentThreadId")
	procLoadCursorW             = user32.NewProc("LoadCursorW")
	procLoadImageW              = user32.NewProc("LoadImageW")
	procShowWindow              = user32.NewProc("ShowWindow")
	procSetForegroundWindow     = user32.NewProc("SetForegroundWindow")
	procSetWindowPos            = user32.NewProc("SetWindowPos")
	procGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	procCreatePopupMenu         = user32.NewProc("CreatePopupMenu")
	procAppendMenuW             = user32.NewProc("AppendMenuW")
	procTrackPopupMenu          = user32.NewProc("TrackPopupMenu")
	procDestroyMenu             = user32.NewProc("DestroyMenu")
	procGetCursorPos            = user32.NewProc("GetCursorPos")
	procSetTimer                = user32.NewProc("SetTimer")
	procKillTimer               = user32.NewProc("KillTimer")
	procCreateWindowExWText     = user32.NewProc("CreateWindowExW")
	procSetWindowTextW          = user32.NewProc("SetWindowTextW")
	procGetWindowTextW          = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW    = user32.NewProc("GetWindowTextLengthW")
	procSendDlgItemMessageW     = user32.NewProc("SendDlgItemMessageW")
	procSendInput               = user32.NewProc("SendInput")
	procShellNotifyIconW        = shell32.NewProc("Shell_NotifyIconW")
	procGetDC                   = user32.NewProc("GetDC")
	procReleaseDC               = user32.NewProc("ReleaseDC")
	procGetSysColorBrush        = user32.NewProc("GetSysColorBrush")
	procSetWindowDisplayAffn    = user32.NewProc("SetWindowDisplayAffinity")
	procEnumDisplayMonitors     = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW         = user32.NewProc("GetMonitorInfoW")
	procLockWorkStation         = user32.NewProc("LockWorkStation")
	procSetProcessDpiAwareness2 = user32.NewProc("SetProcessDpiAwarenessContext")
	procUpdateWindow            = user32.NewProc("UpdateWindow")
	procInvalidateRect          = user32.NewProc("InvalidateRect")
	procGetClientRect           = user32.NewProc("GetClientRect")
	procMoveWindow              = user32.NewProc("MoveWindow")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

// Window messages and constants.
const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmTimer         = 0x0113
	wmUser          = 0x0400
	wmApp           = 0x8000
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmPaint         = 0x000F
	wmSetFont       = 0x0030
	wmQuit          = 0x0012
	wmContextMenu   = 0x007B
	wmTrayCallback  = wmUser + 1
	wmCtlColorStatc = 0x0138

	// Custom app messages posted from worker goroutines to the UI thread.
	appConsent   = wmApp + 1
	appChat      = wmApp + 2
	appNotify    = wmApp + 3
	appReboot    = wmApp + 4
	appStatus    = wmApp + 5
	appBannerOn  = wmApp + 6
	appBannerOff = wmApp + 7
	appQuit      = wmApp + 8
	appChatSeen  = wmApp + 9

	swHide        = 0
	swShow        = 5
	swShowNormal  = 1
	swShowNoActiv = 8

	cwUseDefault = ^uint32(0x7fffffff) // 0x80000000 (CW_USEDEFAULT)

	colorWindow    = 5
	colorBtnFace   = 15
	idcArrow       = 32512
	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	smCxScreen       = 0
	smCyScreen       = 1
	smXVirtualScreen = 76
	smYVirtualScreen = 77
	smCxVirtualScr   = 78
	smCyVirtualScr   = 79
	smCMonitors      = 80

	// Window styles.
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	wsBorder       = 0x00800000
	wsVScroll      = 0x00200000
	wsTabStop      = 0x00010000
	wsGroup        = 0x00020000
	wsPopup        = 0x80000000
	wsOverlappedWn = 0x00CF0000
	esMultiline    = 0x0004
	esReadonly     = 0x0800
	esAutoVScroll  = 0x0040
	esAutoHScroll  = 0x0080
	bsDefPushButt  = 0x0001
	bsPushButton   = 0x0000
	ssLeft         = 0x0000

	wsExTopmost    = 0x00000008
	wsExToolwindow = 0x00000080
	wsExNoActivate = 0x08000000

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	hwndTopmost   = ^uintptr(0) // (HWND)-1
	hwndTop       = 0

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmRightBtn  = 0x0002
	tpmReturnCmd = 0x0100

	// DPI awareness context (PER_MONITOR_AWARE_V2 == -4).
	dpiPerMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4

	// SetWindowDisplayAffinity.
	wdaExcludeFromCapture = 0x00000011

	// BitBlt.
	srccopy    = 0x00CC0020
	captureBlt = 0x40000000

	// GetDIBits.
	dibRGBColors = 0
	biRGB        = 0

	// SendInput event types/flags.
	inputMouse            = 0
	inputKeyboard         = 1
	mouseEventFMove       = 0x0001
	mouseEventFLeftDown   = 0x0002
	mouseEventFLeftUp     = 0x0004
	mouseEventFRightDown  = 0x0008
	mouseEventFRightUp    = 0x0010
	mouseEventFMiddleDown = 0x0020
	mouseEventFMiddleUp   = 0x0040
	mouseEventFWheel      = 0x0800
	mouseEventFAbsolute   = 0x8000
	mouseEventFVirtualDsk = 0x4000
	keyEventFKeyUp        = 0x0002
	keyEventFScancode     = 0x0008
	keyEventFExtendedKey  = 0x0001
	keyEventFUnicode      = 0x0004

	wheelDelta = 120
)

// point mirrors POINT.
type point struct{ X, Y int32 }

// msg mirrors MSG.
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

// wndClassEx mirrors WNDCLASSEXW.
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// notifyIconData mirrors NOTIFYICONDATAW.
type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

const (
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	nifState   = 0x00000008

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	niifNone    = 0x00000000
	niifInfo    = 0x00000001
	niifWarning = 0x00000002
	niifError   = 0x00000003
)

// mouseInput is an INPUT carrying a MOUSEINPUT (40 bytes on 64-bit).
type mouseInput struct {
	Type        uint32
	_           uint32
	Dx          int32
	Dy          int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	_           uint32
	DwExtraInfo uintptr
}

// keybdInput is an INPUT carrying a KEYBDINPUT, padded to the INPUT size.
type keybdInput struct {
	Type        uint32
	_           uint32
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
	_           uint64 // pad to the 40-byte INPUT size
}

// bitmapInfoHeader mirrors BITMAPINFOHEADER.
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// bitmapInfo is BITMAPINFO with a BITMAPINFOHEADER and a single colour slot.
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// monitorInfo mirrors MONITORINFO.
type monitorInfo struct {
	CbSize    uint32
	RcMonitor windows.Rect
	RcWork    windows.Rect
	DwFlags   uint32
}

// ---- small wrappers ----

func utf16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func defWindowProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(m), wp, lp)
	return r
}

func postMessage(hwnd uintptr, m uint32, wp, lp uintptr) bool {
	r, _, _ := procPostMessageW.Call(hwnd, uintptr(m), wp, lp)
	return r != 0
}

func sendMessage(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(hwnd, uintptr(m), wp, lp)
	return r
}

func getSystemMetrics(index int32) int32 {
	r, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int32(r)
}

func loIntN(v uintptr) int32  { return int32(int16(v & 0xffff)) }
func hiIntN(v uintptr) int32  { return int32(int16((v >> 16) & 0xffff)) }
func loword(v uintptr) uint16 { return uint16(v & 0xffff) }
func hiword(v uintptr) uint16 { return uint16((v >> 16) & 0xffff) }
