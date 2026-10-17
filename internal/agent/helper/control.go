package helper

import "encoding/json"

// ctrlMsg is a control message received from the viewer over the media channel
// (text frames, see docs/api.md section 4).
type ctrlMsg struct {
	T    string `json:"t"`
	Seq  uint32 `json:"seq"`  // ack
	A    string `json:"a"`    // mouse/key action
	X    int    `json:"x"`    // mouse x (selected-screen pixels)
	Y    int    `json:"y"`    // mouse y
	B    int    `json:"b"`    // mouse button: 0 left, 1 middle, 2 right
	D    int    `json:"d"`    // wheel delta
	Code string `json:"code"` // KeyboardEvent.code
	Key  string `json:"key"`  // KeyboardEvent.key
	S    string `json:"s"`    // text to type
	Q    int    `json:"q"`    // jpeg quality 10..90
	I    int    `json:"i"`    // monitor index
}

// Control message types.
const (
	ctrlAck     = "ack"
	ctrlMouse   = "mouse"
	ctrlKey     = "key"
	ctrlText    = "text"
	ctrlQuality = "quality"
	ctrlMonitor = "monitor"
	ctrlCAD     = "cad"
	ctrlRefresh = "refresh"
)

// parseCtrl decodes a control message.
func parseCtrl(b []byte) (ctrlMsg, error) {
	var m ctrlMsg
	err := json.Unmarshal(b, &m)
	return m, err
}

// clampQuality constrains a requested JPEG quality to the 10..90 range.
func clampQuality(q int) int {
	if q < 10 {
		return 10
	}
	if q > 90 {
		return 90
	}
	return q
}

// defaultQuality is the starting JPEG quality.
const defaultQuality = 60
