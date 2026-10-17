package helper

import "testing"

func TestParseCtrlMouse(t *testing.T) {
	m, err := parseCtrl([]byte(`{"t":"mouse","a":"move","x":100,"y":200,"b":0,"d":-120}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.T != ctrlMouse || m.A != "move" || m.X != 100 || m.Y != 200 || m.D != -120 {
		t.Fatalf("mouse parse: %+v", m)
	}
}

func TestParseCtrlKeyTextQualityMonitor(t *testing.T) {
	if m, _ := parseCtrl([]byte(`{"t":"key","a":"down","code":"KeyA","key":"a"}`)); m.T != ctrlKey || m.Code != "KeyA" {
		t.Fatalf("key: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"text","s":"hello"}`)); m.T != ctrlText || m.S != "hello" {
		t.Fatalf("text: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"quality","q":80}`)); m.T != ctrlQuality || m.Q != 80 {
		t.Fatalf("quality: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"monitor","i":1}`)); m.T != ctrlMonitor || m.I != 1 {
		t.Fatalf("monitor: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"ack","seq":42}`)); m.T != ctrlAck || m.Seq != 42 {
		t.Fatalf("ack: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"cad"}`)); m.T != ctrlCAD {
		t.Fatalf("cad: %+v", m)
	}
	if m, _ := parseCtrl([]byte(`{"t":"refresh"}`)); m.T != ctrlRefresh {
		t.Fatalf("refresh: %+v", m)
	}
}

func TestClampQuality(t *testing.T) {
	if clampQuality(5) != 10 {
		t.Fatal("below min")
	}
	if clampQuality(100) != 90 {
		t.Fatal("above max")
	}
	if clampQuality(60) != 60 {
		t.Fatal("in range")
	}
}
