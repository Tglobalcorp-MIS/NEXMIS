//go:build windows

package helper

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const idBannerStop = 1

// openBanner shows the top-centre "remote in progress" banner. It is excluded
// from screen capture so it never appears in the stream the viewer sees.
func (u *ui) openBanner(mode, requester string) {
	defer recoverPanic(u.log, "openBanner")
	if u.bannerHwnd != 0 {
		u.closeBanner()
	}
	const w, h = 480, 44
	hwnd, dpi := createScaledWindow(bannerClass, "",
		wsPopup|wsBorder, wsExTopmost|wsExToolwindow|wsExNoActivate, w, h)
	if hwnd == 0 {
		return
	}
	// Reposition to the top centre of the primary monitor's work area.
	var rc windows.Rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	wa := primaryWorkArea()
	ww, wh := rc.Right-rc.Left, rc.Bottom-rc.Top
	procMoveWindow.Call(hwnd, uintptr(wa.Left+(wa.Right-wa.Left-ww)/2), uintptr(wa.Top+dp(8, dpi)), uintptr(ww), uintptr(wh), 0)

	bg := uint32(clrBannerView)
	if mode == "control" {
		bg = clrBannerCtrl
	}
	s := newSkin(hwnd, dpi, bg)
	s.setIcon(appIconSized(dp(20, dpi)), 12, 12, 20)
	s.iconPlate = true
	text := fmt.Sprintf("MIS（%s）正在%s您的畫面", requester, modeVerb(mode))
	s.label(hwnd, text, 40, 12, w-40-128, 22, 10, fwSemibold, clrWhite, bg, ssEndEllipsis)
	s.button(hwnd, "中斷連線", w-116, 7, 104, 30, idBannerStop, false, 0, 0)

	// Exclude from capture (Windows 10 2004+); ignore failure on older builds.
	procSetWindowDisplayAffn.Call(hwnd, wdaExcludeFromCapture)

	u.bannerHwnd = hwnd
	procShowWindow.Call(hwnd, swShowNoActiv)
	u.log.Info("remote banner shown", "mode", mode, "requester", requester)
}

func (u *ui) closeBanner() {
	if u.bannerHwnd != 0 {
		procDestroyWindow.Call(u.bannerHwnd)
		u.bannerHwnd = 0
	}
}

func bannerWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	if r, ok := skinProc(hwnd, m, wp, lp); ok {
		return r
	}
	u := theUI
	switch m {
	case wmCommand:
		if uint16(loword(wp)) == idBannerStop && u != nil && u.onBannerEnd != nil {
			u.log.Info("remote banner: user pressed disconnect")
			u.onBannerEnd()
		}
		return 0
	case wmDestroy:
		if u != nil {
			u.bannerHwnd = 0
		}
		dropSkin(hwnd)
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
