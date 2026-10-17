//go:build windows

package discovery

import (
	"fmt"
	"net/netip"
	"syscall"
	"unsafe"
)

var (
	iphlpapi          = syscall.NewLazyDLL("iphlpapi.dll")
	procGetIpNetTable = iphlpapi.NewProc("GetIpNetTable")
)

// mibIPNetRow mirrors MIB_IPNETROW (24 bytes).
type mibIPNetRow struct {
	Index       uint32
	PhysAddrLen uint32
	PhysAddr    [8]byte
	Addr        uint32 // network byte order
	Type        uint32
}

const (
	errInsufficientBuffer = 122
	mibIPNetTypeInvalid   = 2
)

// readARP reads the IPv4 neighbour table via GetIpNetTable.
func readARP() map[netip.Addr]string {
	out := map[netip.Addr]string{}
	var size uint32
	r, _, _ := procGetIpNetTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if r != errInsufficientBuffer || size == 0 {
		return out
	}
	buf := make([]byte, size)
	r, _, _ = procGetIpNetTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 {
		return out
	}
	n := *(*uint32)(unsafe.Pointer(&buf[0]))
	rowSize := unsafe.Sizeof(mibIPNetRow{})
	for i := uint32(0); i < n; i++ {
		off := 4 + uintptr(i)*rowSize
		if off+rowSize > uintptr(len(buf)) {
			break
		}
		row := (*mibIPNetRow)(unsafe.Pointer(&buf[off]))
		if row.Type == mibIPNetTypeInvalid || row.PhysAddrLen != 6 {
			continue
		}
		a := row.Addr
		addr := netip.AddrFrom4([4]byte{byte(a), byte(a >> 8), byte(a >> 16), byte(a >> 24)})
		p := row.PhysAddr
		if p == [8]byte{} {
			continue
		}
		out[addr] = fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", p[0], p[1], p[2], p[3], p[4], p[5])
	}
	return out
}
