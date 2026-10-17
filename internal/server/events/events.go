// Package events is an in-process publish/subscribe bus used to push live
// updates to connected admin browsers.
package events

import (
	"encoding/json"
	"sync"
)

// Event types pushed to the UI (see docs/api.md §5).
const (
	DeviceOnline      = "device.online"
	DeviceOffline     = "device.offline"
	DeviceUpdated     = "device.updated"
	CommandUpdated    = "command.updated"
	ChatMessage       = "chat.message"
	RemoteUpdated     = "remote.updated"
	DeploymentUpdated = "deployment.updated"
	ScanUpdated       = "scan.updated"
)

type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Bus fans out events to subscribers. Slow subscribers drop events rather
// than block publishers.
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
}

func (b *Bus) Publish(typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	ev := Event{Type: typ, Data: raw}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
