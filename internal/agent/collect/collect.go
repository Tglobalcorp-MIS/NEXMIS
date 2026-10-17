// Package collect gathers inventory, health and battery data from the local
// machine. The Windows implementation is the production one; other
// platforms get a best-effort implementation so the agent can be developed
// and tested on Linux.
package collect

import (
	"context"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Identity describes the machine for enrollment.
type Identity struct {
	MachineID string
	Hostname  string
	Domain    string
	OS        string
	OSVersion string
}

// Collector is implemented per platform. Every method must be safe to call
// concurrently and must honour ctx cancellation; a collector that fails
// partially should return what it has plus a nil error, and only return an
// error when nothing useful could be collected.
type Collector interface {
	Identity(ctx context.Context) (Identity, error)
	// Heartbeat returns cheap, frequently sampled metrics. AgentVersion and
	// HelperOnline are filled in by the caller.
	Heartbeat(ctx context.Context) (protocol.Heartbeat, error)
	Hardware(ctx context.Context) (*protocol.Hardware, error)
	Software(ctx context.Context) ([]protocol.Software, error)
	// Health returns a diagnostic snapshot including computed Issues.
	Health(ctx context.Context) (*protocol.Health, error)
	// Battery returns Present=false on machines without a battery.
	Battery(ctx context.Context) (*protocol.Battery, error)
}
