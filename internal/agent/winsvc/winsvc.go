// Package winsvc wraps running as (and managing) a Windows service. On
// Windows it uses the Service Control Manager via golang.org/x/sys/windows/svc
// and .../svc/mgr; on other platforms IsService reports false, Run executes in
// the foreground, and the management functions return an "only supported on
// Windows" error so the agent can still be developed and tested on Linux.
package winsvc

import "errors"

// ErrWindowsOnly is returned by service-management functions off Windows.
var ErrWindowsOnly = errors.New("only supported on Windows")
