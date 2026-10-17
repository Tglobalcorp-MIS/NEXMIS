package helper

import (
	"encoding/binary"
	"encoding/json"
)

// Remote-viewing binary frame types (see docs/api.md section 4). The helper is
// the producer of these frames; the viewer in the browser decodes them.
const (
	frameScreenInfo byte = 0x01 // + UTF-8 JSON {width,height,monitor,monitors}
	frameBlock      byte = 0x02 // + u16 x,y,w,h (big-endian) + JPEG bytes
	frameEnd        byte = 0x03 // + u32 seq (big-endian)
	frameCursor     byte = 0x04 // + u16 x,y (big-endian)
)

// screenInfo is the JSON payload of a 0x01 frame.
type screenInfo struct {
	Width    int `json:"width"`
	Height   int `json:"height"`
	Monitor  int `json:"monitor"`
	Monitors int `json:"monitors"`
}

// encodeScreenInfo builds a 0x01 screen-info frame.
func encodeScreenInfo(width, height, monitor, monitors int) []byte {
	js, _ := json.Marshal(screenInfo{Width: width, Height: height, Monitor: monitor, Monitors: monitors})
	out := make([]byte, 1+len(js))
	out[0] = frameScreenInfo
	copy(out[1:], js)
	return out
}

// encodeBlock builds a 0x02 block frame: header then JPEG bytes.
func encodeBlock(x, y, w, h uint16, jpeg []byte) []byte {
	out := make([]byte, 1+8+len(jpeg))
	out[0] = frameBlock
	binary.BigEndian.PutUint16(out[1:], x)
	binary.BigEndian.PutUint16(out[3:], y)
	binary.BigEndian.PutUint16(out[5:], w)
	binary.BigEndian.PutUint16(out[7:], h)
	copy(out[9:], jpeg)
	return out
}

// encodeFrameEnd builds a 0x03 frame-end frame carrying the sequence number.
func encodeFrameEnd(seq uint32) []byte {
	out := make([]byte, 1+4)
	out[0] = frameEnd
	binary.BigEndian.PutUint32(out[1:], seq)
	return out
}

// encodeCursor builds a 0x04 cursor-position frame.
func encodeCursor(x, y uint16) []byte {
	out := make([]byte, 1+4)
	out[0] = frameCursor
	binary.BigEndian.PutUint16(out[1:], x)
	binary.BigEndian.PutUint16(out[3:], y)
	return out
}
