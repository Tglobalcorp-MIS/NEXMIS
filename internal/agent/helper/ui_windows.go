//go:build windows

package helper

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// ui owns the helper's single UI thread: the message loop, the hidden main
// window, the tray icon, and the modeless feature windows. Worker goroutines
// never touch Win32 directly; they marshal work to the UI thread by storing a
// payload in a registry and posting a custom message to the main window.
type ui struct {
	log *slog.Logger

	threadID uintptr
	mainHwnd uintptr
	hIcon    uintptr

	status      helperipc.Status
	statusMu    sync.Mutex
	curUser     string
	onContact   func() // open chat (set by the helper)
	onBannerEnd func() // disconnect pressed on the banner
	onChatSend  func(body string)
	sendIPC     func(t, id string, p any) error

	chatHwnd   uintptr
	bannerHwnd uintptr
	chatLog    []chatLine
	lastChatID string
	chatMu     sync.Mutex
}

type chatLine struct {
	sender string
	body   string
	fromMe bool
}

var theUI *ui

// payload registry: Go values referenced from the UI thread by integer id.
var (
	payloadMu  sync.Mutex
	payloads   = map[uint64]any{}
	payloadSeq uint64
)

func putPayload(v any) uint64 {
	id := atomic.AddUint64(&payloadSeq, 1)
	payloadMu.Lock()
	payloads[id] = v
	payloadMu.Unlock()
	return id
}

func takePayload(id uint64) any {
	payloadMu.Lock()
	v := payloads[id]
	delete(payloads, id)
	payloadMu.Unlock()
	return v
}

// consentJob and rebootJob carry a request and the channel to answer it on.
type consentJob struct {
	req    helperipc.ConsentRequest
	result chan helperipc.ConsentResult
}

type rebootJob struct {
	req    helperipc.RebootPrompt
	result chan helperipc.RebootResponse
}

const (
	idmContact = 1001
	idmAbout   = 1002
	trayUID    = 1
)

func newUI(log *slog.Logger) *ui {
	return &ui{log: log}
}

// user returns the logged-on user the helper reports.
func (u *ui) user() string { return u.curUser }

// run registers classes, creates the main window and tray icon, and pumps the
// message loop until quit. It must own its OS thread for the lifetime of the
// UI (all Win32 calls happen here or via posted messages).
func (u *ui) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer recoverPanic(u.log, "ui.run")

	theUI = u
	tid, _, _ := procGetCurrentThreadId.Call()
	u.threadID = tid

	u.hIcon = loadAppIcon()
	u.registerClasses()
	u.createMain()
	u.addTray()
	defer u.removeTray()

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// loadAppIcon writes the embedded .ico to disk and loads it as an HICON.
func loadAppIcon() uintptr {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, "NEXMIS", "helper.ico")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, appIcon, 0o644)
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(utf16(p))),
		imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	return h
}

var (
	mainClassName  = utf16("NexmisHelperMain")
	consentClass   = utf16("NexmisHelperConsent")
	chatClass      = utf16("NexmisHelperChat")
	rebootClass    = utf16("NexmisHelperReboot")
	bannerClass    = utf16("NexmisHelperBanner")
	classesRegOnce sync.Once
	mainProcCB     = syscall.NewCallback(mainWndProc)
	consentProcCB  = syscall.NewCallback(consentWndProc)
	chatProcCB     = syscall.NewCallback(chatWndProc)
	rebootProcCB   = syscall.NewCallback(rebootWndProc)
	bannerProcCB   = syscall.NewCallback(bannerWndProc)
)

func (u *ui) registerClasses() {
	classesRegOnce.Do(func() {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		reg := func(name *uint16, proc uintptr, bg uintptr) {
			wc := wndClassEx{
				Size:       uint32(unsafe.Sizeof(wndClassEx{})),
				WndProc:    proc,
				Icon:       u.hIcon,
				IconSm:     u.hIcon,
				Cursor:     cursor,
				Background: bg,
				ClassName:  name,
			}
			procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		}
		winBrush, _, _ := procGetSysColorBrush.Call(colorWindow)
		faceBrush, _, _ := procGetSysColorBrush.Call(colorBtnFace)
		reg(mainClassName, mainProcCB, 0)
		reg(consentClass, consentProcCB, faceBrush)
		reg(chatClass, chatProcCB, winBrush)
		reg(rebootClass, rebootProcCB, faceBrush)
		reg(bannerClass, bannerProcCB, faceBrush)
	})
}

func (u *ui) createMain() {
	h, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(mainClassName)),
		uintptr(unsafe.Pointer(utf16("NEXMIS"))),
		wsOverlapped,
		0, 0, 0, 0,
		0, 0, 0, 0)
	u.mainHwnd = h
}

// ---- tray ----

func (u *ui) trayData(flags uint32) notifyIconData {
	n := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             u.mainHwnd,
		UID:              trayUID,
		UFlags:           flags,
		UCallbackMessage: wmTrayCallback,
		HIcon:            u.hIcon,
	}
	return n
}

func (u *ui) addTray() {
	n := u.trayData(nifMessage | nifIcon | nifTip)
	copyUTF16(n.SzTip[:], u.tooltip())
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&n)))
}

func (u *ui) removeTray() {
	n := u.trayData(0)
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&n)))
}

func (u *ui) updateTrayTip() {
	n := u.trayData(nifTip)
	copyUTF16(n.SzTip[:], u.tooltip())
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func (u *ui) tooltip() string {
	u.statusMu.Lock()
	st := u.status
	u.statusMu.Unlock()
	if st.Connected {
		return fmt.Sprintf("NEXMIS 已連線\n%s", st.ServerURL)
	}
	return "NEXMIS 未連線"
}

// showBalloon displays a tray balloon notification.
func (u *ui) showBalloon(title, message string, level string) {
	n := u.trayData(nifInfo)
	copyUTF16(n.SzInfoTitle[:], title)
	copyUTF16(n.SzInfo[:], message)
	switch level {
	case "warning":
		n.DwInfoFlags = niifWarning
	case "critical":
		n.DwInfoFlags = niifError
	default:
		n.DwInfoFlags = niifInfo
	}
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func (u *ui) showTrayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	procAppendMenuW.Call(menu, mfString, idmContact, uintptr(unsafe.Pointer(utf16("聯絡 MIS"))))
	procAppendMenuW.Call(menu, mfString, idmAbout, uintptr(unsafe.Pointer(utf16("關於 NEXMIS"))))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Required so the menu dismisses correctly.
	procSetForegroundWindow.Call(u.mainHwnd)
	procTrackPopupMenu.Call(menu, tpmRightBtn, uintptr(pt.X), uintptr(pt.Y), 0, u.mainHwnd, 0)
}

func (u *ui) showAbout() {
	u.statusMu.Lock()
	st := u.status
	u.statusMu.Unlock()
	state := "未連線"
	if st.Connected {
		state = "已連線 (" + st.ServerURL + ")"
	}
	body := fmt.Sprintf("NEXMIS 使用者端助手\n版本：%s\n狀態：%s", Version, state)
	messageBox(u.mainHwnd, body, "關於 NEXMIS")
}

// ---- cross-thread entry points (called from worker goroutines) ----

func (u *ui) requestConsent(req helperipc.ConsentRequest) helperipc.ConsentResult {
	job := &consentJob{req: req, result: make(chan helperipc.ConsentResult, 1)}
	id := putPayload(job)
	postMessage(u.mainHwnd, appConsent, uintptr(id), 0)
	return <-job.result
}

func (u *ui) requestReboot(req helperipc.RebootPrompt) helperipc.RebootResponse {
	job := &rebootJob{req: req, result: make(chan helperipc.RebootResponse, 1)}
	id := putPayload(job)
	postMessage(u.mainHwnd, appReboot, uintptr(id), 0)
	return <-job.result
}

func (u *ui) notify(n helperipc.Notify) {
	id := putPayload(n)
	postMessage(u.mainHwnd, appNotify, uintptr(id), 0)
}

func (u *ui) incomingChat(m helperipc.ChatMessage) {
	id := putPayload(m)
	postMessage(u.mainHwnd, appChat, uintptr(id), 0)
}

func (u *ui) setStatus(st helperipc.Status) {
	u.statusMu.Lock()
	u.status = st
	u.statusMu.Unlock()
	postMessage(u.mainHwnd, appStatus, 0, 0)
}

func (u *ui) showBanner(mode, requester string) {
	id := putPayload([2]string{mode, requester})
	postMessage(u.mainHwnd, appBannerOn, uintptr(id), 0)
}

func (u *ui) hideBanner() { postMessage(u.mainHwnd, appBannerOff, 0, 0) }

func (u *ui) quit() { postMessage(u.mainHwnd, appQuit, 0, 0) }

// ---- main window proc ----

func mainWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	u := theUI
	if u == nil {
		return defWindowProc(hwnd, m, wp, lp)
	}
	switch m {
	case wmTrayCallback:
		switch uint32(loword(lp)) {
		case wmRButtonUp, wmContextMenu:
			u.showTrayMenu()
		case wmLButtonUp:
			if u.onContact != nil {
				u.onContact()
			}
		}
		return 0
	case wmCommand:
		switch uint16(loword(wp)) {
		case idmContact:
			if u.onContact != nil {
				u.onContact()
			}
		case idmAbout:
			u.showAbout()
		}
		return 0
	case appConsent:
		if job, ok := takePayload(uint64(wp)).(*consentJob); ok {
			u.openConsentWindow(job)
		}
		return 0
	case appReboot:
		if job, ok := takePayload(uint64(wp)).(*rebootJob); ok {
			u.openRebootWindow(job)
		}
		return 0
	case appNotify:
		if n, ok := takePayload(uint64(wp)).(helperipc.Notify); ok {
			u.showBalloon(n.Title, n.Message, n.Level)
		}
		return 0
	case appChat:
		if cm, ok := takePayload(uint64(wp)).(helperipc.ChatMessage); ok {
			u.handleIncomingChat(cm)
		}
		return 0
	case appStatus:
		u.updateTrayTip()
		return 0
	case appBannerOn:
		if v, ok := takePayload(uint64(wp)).([2]string); ok {
			u.openBanner(v[0], v[1])
		}
		return 0
	case appBannerOff:
		u.closeBanner()
		return 0
	case appQuit:
		procPostQuitMessage.Call(0)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}

// ---- small helpers ----

func copyUTF16(dst []uint16, s string) {
	src, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	n := copy(dst, src)
	if n < len(dst) {
		dst[n] = 0
	} else if len(dst) > 0 {
		dst[len(dst)-1] = 0
	}
}

var procMessageBoxW = user32.NewProc("MessageBoxW")

const mbTopmost = 0x00040000

func messageBox(parent uintptr, text, title string) {
	procMessageBoxW.Call(parent,
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(unsafe.Pointer(utf16(title))),
		mbTopmost)
}
