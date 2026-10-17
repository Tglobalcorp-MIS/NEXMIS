//go:build linux

package collect

import "syscall"

// statfsSizes returns total and free bytes of the filesystem at path.
func statfsSizes(path string) (size, free uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := uint64(st.Frsize)
	if bs == 0 {
		bs = uint64(st.Bsize)
	}
	return st.Blocks * bs, st.Bfree * bs, true
}
