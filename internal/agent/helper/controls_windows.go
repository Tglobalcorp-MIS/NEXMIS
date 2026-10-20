//go:build windows

package helper

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetStockObject = gdi32.NewProc("GetStockObject")
	staticClassName    = utf16("STATIC")
	buttonClassName    = utf16("BUTTON")
	editClassName      = utf16("EDIT")
)

const defaultGUIFont = 17

// guiFont returns the system default GUI font handle.
func guiFont() uintptr {
	h, _, _ := procGetStockObject.Call(defaultGUIFont)
	return h
}

// createControl creates a child control and applies the GUI font.
func createControl(parent uintptr, class *uint16, text string, style uint32, x, y, w, h int32, id uintptr) uintptr {
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, 0, 0)
	if hwnd != 0 {
		sendMessage(hwnd, wmSetFont, guiFont(), 1)
	}
	return hwnd
}

// sendMessageStr sends a message whose lParam is a UTF-16 string pointer.
func sendMessageStr(hwnd uintptr, m uint32, wp uintptr, s string) uintptr {
	return sendMessage(hwnd, m, wp, uintptr(unsafe.Pointer(utf16(s))))
}

// setText sets a window/control caption.
func setText(hwnd uintptr, s string) {
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(utf16(s))))
}

// getText reads a control's text.
func getText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}
