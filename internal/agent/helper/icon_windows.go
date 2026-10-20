//go:build windows

package helper

import _ "embed"

// appIcon is the tray/window icon: the T-Global company mark as a 32bpp .ico
// (16–64px, transparent background) made from the logo on tglobalcorp.com.
//
//go:embed appicon.ico
var appIcon []byte
