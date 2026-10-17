package discovery

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// NetBIOSName sends an NBSTAT node-status query (UDP 137) and returns the
// workstation name, or "" if the host does not answer.
func NetBIOSName(ip string, timeout time.Duration) string {
	conn, err := net.DialTimeout("udp4", net.JoinHostPort(ip, "137"), timeout)
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(nbstatQuery()); err != nil {
		return ""
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	return parseNBSTAT(buf[:n])
}

func nbstatQuery() []byte {
	q := make([]byte, 0, 50)
	id := make([]byte, 2)
	_, _ = rand.Read(id)
	q = append(q, id...)
	q = append(q, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // flags, 1 question
	// Encoded wildcard name "*" padded with NULs (first-level encoding).
	q = append(q, 0x20)
	name := make([]byte, 16)
	name[0] = '*'
	for _, b := range name {
		q = append(q, 'A'+(b>>4), 'A'+(b&0x0f))
	}
	q = append(q, 0x00)
	q = append(q, 0x00, 0x21, 0x00, 0x01) // type NBSTAT, class IN
	return q
}

func parseNBSTAT(b []byte) string {
	// Header (12) + name (34) + type/class (4) + TTL (4) + rdlength (2) = 56.
	if len(b) < 57 {
		return ""
	}
	if binary.BigEndian.Uint16(b[6:8]) == 0 { // no answers
		return ""
	}
	off := 56
	count := int(b[off])
	off++
	for i := 0; i < count && off+18 <= len(b); i++ {
		name := strings.TrimRight(string(b[off:off+15]), " \x00")
		suffix := b[off+15]
		flags := binary.BigEndian.Uint16(b[off+16 : off+18])
		off += 18
		// Suffix 0x00 unique (not group) name is the workstation name.
		if suffix == 0x00 && flags&0x8000 == 0 && name != "" {
			return name
		}
	}
	return ""
}
