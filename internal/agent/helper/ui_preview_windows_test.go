//go:build windows

package helper

import (
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

var (
	procPrintWindow = user32.NewProc("PrintWindow")
	procGetDlgItem  = user32.NewProc("GetDlgItem")
)

const vkTab = 0x09

// TestUIPreview opens each helper window on the real desktop and saves a PNG
// of it, for reviewing the look by eye. It is skipped unless
// NEXMIS_UI_PREVIEW names an output directory:
//
//	$env:NEXMIS_UI_PREVIEW="$PWD\preview"; go test -run TestUIPreview ./internal/agent/helper
func TestUIPreview(t *testing.T) {
	out := os.Getenv("NEXMIS_UI_PREVIEW")
	if out == "" {
		t.Skip("set NEXMIS_UI_PREVIEW to an output directory to render the helper windows")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	procSetProcessDpiAwareness2.Call(dpiPerMonitorV2)
	u := newUI(slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.curUser = `CORP\preview`
	u.onContact = u.openChat
	go u.run()
	waitFor(t, func() bool { return u.mainHwnd != 0 })
	defer u.quit()

	// shootWith captures the window find returns, then dismisses it with close.
	shootWith := func(name string, find func() uintptr, close func(h uintptr)) {
		t.Helper()
		var h uintptr
		waitFor(t, func() bool { h = find(); return h != 0 })
		procSetWindowDisplayAffn.Call(h, 0) // the banner hides itself from capture
		time.Sleep(800 * time.Millisecond)  // let it paint
		if name != "" {
			if err := savePNG(h, filepath.Join(out, name+".png")); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		close(h)
		time.Sleep(300 * time.Millisecond)
	}
	shoot := func(name string, find func() uintptr) {
		t.Helper()
		shootWith(name, find, func(h uintptr) { postMessage(h, wmClose, 0, 0) })
	}
	anyConsent := func() uintptr {
		consentStatesMu.Lock()
		defer consentStatesMu.Unlock()
		for h := range consentStates {
			return h
		}
		return 0
	}
	// key posts a key press to a consent button, as if typed while it had focus.
	key := func(id, vk uintptr) func(h uintptr) {
		return func(h uintptr) {
			btn, _, _ := procGetDlgItem.Call(h, id)
			postMessage(btn, wmKeyDown, vk, 0)
		}
	}

	// Each case checks the keyboard behaviour while rendering the window.
	cases := []struct {
		name, mode  string
		close       func(h uintptr)
		granted     bool
		reason      string
		description string
	}{
		{"consent-view", "view", key(idConsentDeny, vkReturn), false, "denied", "Enter on the default (拒絕) button"},
		{"consent-control", "control", key(idConsentDeny, vkEscape), false, "denied", "Esc"},
		{"", "control", func(h uintptr) {
			deny, _, _ := procGetDlgItem.Call(h, idConsentDeny)
			allow, _, _ := procGetDlgItem.Call(h, idConsentAllow)
			postMessage(deny, wmKeyDown, vkTab, 0) // Tab moves focus to 允許
			postMessage(allow, wmKeyDown, vkReturn, 0)
		}, true, "", "Tab then Enter"},
	}
	for _, c := range cases {
		res := make(chan helperipc.ConsentResult, 1)
		go func() {
			res <- u.requestConsent(helperipc.ConsentRequest{SessionID: "preview-" + c.mode, Mode: c.mode,
				Requester: "王小明", Reason: "協助排除 Outlook 無法收信的問題", TimeoutSec: 60})
		}()
		shootWith(c.name, anyConsent, c.close)
		select {
		case r := <-res:
			if r.Granted != c.granted || r.Reason != c.reason {
				t.Errorf("%s: got granted=%v reason=%q, want %v %q", c.description, r.Granted, r.Reason, c.granted, c.reason)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("%s: consent window did not answer", c.description)
		}
	}

	go u.requestReboot(helperipc.RebootPrompt{ID: "preview", CountdownSec: 300, AllowDefer: true, DefersLeft: 2,
		Message: "安裝 Windows 每月安全性更新後需要重新啟動。"})
	shoot("reboot", func() uintptr {
		rebootStatesMu.Lock()
		defer rebootStatesMu.Unlock()
		for h := range rebootStates {
			return h
		}
		return 0
	})

	for _, mode := range []string{"view", "control"} {
		u.showBanner(mode, "王小明")
		shoot("banner-"+mode, func() uintptr { return u.bannerHwnd })
	}

	u.incomingChat(helperipc.ChatMessage{MessageID: "m1", Sender: "MIS 王小明", Body: "您好，請問現在方便遠端協助嗎？"})
	postMessage(u.mainHwnd, wmCommand, idmContact, 0)
	shoot("chat", func() uintptr { return u.chatHwnd })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for window")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// savePNG renders hwnd (including its frame) with PrintWindow and writes a PNG.
func savePNG(hwnd uintptr, path string) error {
	var rc windows.Rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)
	bmp, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(w), uintptr(h))
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	procPrintWindow.Call(hwnd, mem, 2) // PW_RENDERFULLCONTENT
	procSelectObject.Call(mem, old)

	bi := bitmapInfo{Header: bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32, Compression: biRGB}}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	buf := make([]byte, int(w)*int(h)*4)
	procGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = buf[i+2], buf[i+1], buf[i], 0xFF
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
