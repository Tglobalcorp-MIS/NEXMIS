//go:build windows

package helper

import "fmt"

const idBannerStop = 1

// openBanner shows the top-centre "remote in progress" banner. It is excluded
// from screen capture so it never appears in the stream the viewer sees.
func (u *ui) openBanner(mode, requester string) {
	defer recoverPanic(u.log, "openBanner")
	if u.bannerHwnd != 0 {
		u.closeBanner()
	}
	const w, h = 460, 44
	hwnd := createTopWindow(bannerClass, "",
		wsPopup|wsBorder, wsExTopmost|wsExToolwindow|wsExNoActivate, w, h)
	if hwnd == 0 {
		return
	}
	// Reposition to the top centre of the primary screen.
	sx := getSystemMetrics(smCxScreen)
	procMoveWindow.Call(hwnd, uintptr((sx-w)/2), 8, w, h, 1)

	text := fmt.Sprintf("MIS（%s）正在%s您的畫面", requester, modeVerb(mode))
	createControl(hwnd, staticClassName, text,
		ssLeft|uint32(wsChild)|wsVisible, 12, 12, w-150, 20, 0)
	createControl(hwnd, buttonClassName, "中斷連線",
		uint32(wsChild)|wsVisible|bsPushButton, w-120, 7, 108, 28, idBannerStop)

	// Exclude from capture (Windows 10 2004+); ignore failure on older builds.
	procSetWindowDisplayAffn.Call(hwnd, wdaExcludeFromCapture)

	u.bannerHwnd = hwnd
	procShowWindow.Call(hwnd, swShowNoActiv)
}

func (u *ui) closeBanner() {
	if u.bannerHwnd != 0 {
		procDestroyWindow.Call(u.bannerHwnd)
		u.bannerHwnd = 0
	}
}

func bannerWndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	u := theUI
	switch m {
	case wmCommand:
		if uint16(loword(wp)) == idBannerStop && u != nil && u.onBannerEnd != nil {
			u.onBannerEnd()
		}
		return 0
	case wmDestroy:
		if u != nil {
			u.bannerHwnd = 0
		}
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
