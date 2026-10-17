//go:build !windows && !linux

package collect

// statfsSizes is not implemented on this development platform.
func statfsSizes(path string) (size, free uint64, ok bool) { return 0, 0, false }
