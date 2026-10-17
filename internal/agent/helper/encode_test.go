package helper

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestEncodeScreenInfo(t *testing.T) {
	b := encodeScreenInfo(1920, 1080, 1, 2)
	if b[0] != frameScreenInfo {
		t.Fatalf("type byte = %#x", b[0])
	}
	var si screenInfo
	if err := json.Unmarshal(b[1:], &si); err != nil {
		t.Fatalf("json: %v", err)
	}
	if si.Width != 1920 || si.Height != 1080 || si.Monitor != 1 || si.Monitors != 2 {
		t.Fatalf("bad screen info: %+v", si)
	}
}

func TestEncodeBlock(t *testing.T) {
	jpeg := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	b := encodeBlock(10, 20, 64, 48, jpeg)
	if b[0] != frameBlock {
		t.Fatalf("type byte = %#x", b[0])
	}
	if binary.BigEndian.Uint16(b[1:]) != 10 ||
		binary.BigEndian.Uint16(b[3:]) != 20 ||
		binary.BigEndian.Uint16(b[5:]) != 64 ||
		binary.BigEndian.Uint16(b[7:]) != 48 {
		t.Fatalf("bad header: % x", b[1:9])
	}
	if string(b[9:]) != string(jpeg) {
		t.Fatalf("jpeg payload mismatch")
	}
}

func TestEncodeFrameEnd(t *testing.T) {
	b := encodeFrameEnd(0x01020304)
	if b[0] != frameEnd {
		t.Fatalf("type byte = %#x", b[0])
	}
	if binary.BigEndian.Uint32(b[1:]) != 0x01020304 {
		t.Fatalf("seq = %#x", binary.BigEndian.Uint32(b[1:]))
	}
	if len(b) != 5 {
		t.Fatalf("len = %d", len(b))
	}
}

func TestEncodeCursor(t *testing.T) {
	b := encodeCursor(300, 400)
	if b[0] != frameCursor || len(b) != 5 {
		t.Fatalf("bad cursor frame: % x", b)
	}
	if binary.BigEndian.Uint16(b[1:]) != 300 || binary.BigEndian.Uint16(b[3:]) != 400 {
		t.Fatalf("bad cursor coords")
	}
}
