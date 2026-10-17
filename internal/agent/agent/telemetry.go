package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func (a *Agent) requestRefresh(kind string) {
	select {
	case a.refresh <- kind:
	default:
	}
}

// telemetryLoop schedules collection. Timers keep running while offline,
// but data is only sent when connected; a reconnect triggers a refresh.
func (a *Agent) telemetryLoop(ctx context.Context) {
	last := map[string]time.Time{}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case kind := <-a.refresh:
			switch kind {
			case "connect":
				// Heartbeat now; the rest staggered so a server restart does not
				// receive every agent's full inventory at the same instant.
				a.collect(ctx, "heartbeat")
				last["heartbeat"] = time.Now()
				go func() {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(rand.IntN(20)+2) * time.Second):
					}
					for _, k := range []string{"hardware", "software", "health", "battery"} {
						a.collect(ctx, k)
					}
				}()
				for _, k := range []string{"hardware", "software", "health", "battery"} {
					last[k] = time.Now()
				}
			case "all":
				for _, k := range []string{"heartbeat", "hardware", "software", "health", "battery"} {
					go a.collect(ctx, k)
					last[k] = time.Now()
				}
			default:
				go a.collect(ctx, kind)
				last[kind] = time.Now()
			}
		case <-tick.C:
			if !a.connected.Load() {
				continue
			}
			c := a.config()
			intervals := map[string]int{
				"heartbeat": c.HeartbeatSec, "health": c.HealthSec, "battery": c.BatterySec,
				"hardware": c.HardwareSec, "software": c.SoftwareSec,
			}
			for kind, sec := range intervals {
				if time.Since(last[kind]) >= time.Duration(sec)*time.Second {
					last[kind] = time.Now()
					if kind == "heartbeat" {
						a.collect(ctx, kind)
					} else {
						go a.collect(ctx, kind)
					}
				}
			}
		}
	}
}

var collectLocks = map[string]chan struct{}{
	"heartbeat": make(chan struct{}, 1), "hardware": make(chan struct{}, 1), "software": make(chan struct{}, 1),
	"health": make(chan struct{}, 1), "battery": make(chan struct{}, 1),
}

// collect gathers one kind of telemetry and sends it. Concurrent requests
// for the same kind are coalesced.
func (a *Agent) collect(ctx context.Context, kind string) {
	lock, ok := collectLocks[kind]
	if !ok {
		return
	}
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	default:
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch kind {
	case "heartbeat":
		hb, err := a.col.Heartbeat(cctx)
		if err != nil {
			a.log.Warn("heartbeat collection failed", "err", err)
		}
		hb.Time = time.Now().UTC()
		hb.AgentVersion = a.opt.Version
		hb.HelperOnline = a.host.Online()
		if hb.LoggedInUser == "" {
			hb.LoggedInUser = a.host.User()
		}
		a.send(protocol.THeartbeat, hb)
	case "hardware":
		hw, err := a.col.Hardware(cctx)
		if err != nil || hw == nil {
			a.log.Warn("hardware collection failed", "err", err)
			return
		}
		a.sendInventory(protocol.Inventory{Kind: "hardware", Collected: time.Now().UTC(), Hardware: hw}, hw)
	case "software":
		sw, err := a.col.Software(cctx)
		if err != nil {
			a.log.Warn("software collection failed", "err", err)
			return
		}
		sort.Slice(sw, func(i, j int) bool {
			if sw[i].Name != sw[j].Name {
				return sw[i].Name < sw[j].Name
			}
			return sw[i].Version < sw[j].Version
		})
		if sw == nil {
			sw = []protocol.Software{}
		}
		a.sendInventory(protocol.Inventory{Kind: "software", Collected: time.Now().UTC(), Software: sw}, sw)
	case "health":
		h, err := a.col.Health(cctx)
		if err != nil || h == nil {
			a.log.Warn("health collection failed", "err", err)
			return
		}
		a.send(protocol.THealth, h)
	case "battery":
		b, err := a.col.Battery(cctx)
		if err != nil || b == nil {
			a.log.Warn("battery collection failed", "err", err)
			return
		}
		a.send(protocol.TBattery, b)
	}
}

// sendInventory sends only a hash when the data is unchanged since the
// server last confirmed it; the server asks for the full payload if its
// copy differs.
func (a *Agent) sendInventory(inv protocol.Inventory, data any) {
	b, _ := json.Marshal(data)
	sum := sha256.Sum256(b)
	inv.Hash = hex.EncodeToString(sum[:])
	a.invMu.Lock()
	unchanged := a.invHash[inv.Kind] == inv.Hash
	a.invMu.Unlock()
	if unchanged {
		inv.Hardware, inv.Software, inv.Unchanged = nil, nil, true
	}
	if a.send(protocol.TInventory, inv) {
		a.invMu.Lock()
		a.invHash[inv.Kind] = inv.Hash
		a.invMu.Unlock()
	}
}
