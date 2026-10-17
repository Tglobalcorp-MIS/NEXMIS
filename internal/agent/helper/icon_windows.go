//go:build windows

package helper

import _ "embed"

// appIcon is the tray/window icon (16/32/48px .ico generated at build time).
//
//go:embed appicon.ico
var appIcon []byte
