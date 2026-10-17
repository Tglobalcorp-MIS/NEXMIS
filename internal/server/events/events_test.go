package events

import "testing"

func TestBus(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe()
	b.Publish(DeviceOnline, map[string]string{"device_id": "x"})
	ev := <-ch
	if ev.Type != DeviceOnline || string(ev.Data) != `{"device_id":"x"}` {
		t.Fatalf("got %+v", ev)
	}
	cancel()
	cancel()                   // idempotent
	b.Publish(DeviceOnline, 1) // must not panic after unsubscribe
}
