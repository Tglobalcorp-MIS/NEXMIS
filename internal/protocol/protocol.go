// Package protocol defines the wire protocol shared by nexmis-server and
// nexmis-agent. Every WebSocket frame is a JSON Envelope; unknown message
// types must be ignored by both sides so that versions can evolve
// independently.
package protocol

import (
	"encoding/json"
	"time"
)

// Version is bumped when a backwards-incompatible change is made.
const Version = 1

// Message types. Names are "<area>.<verb>".
const (
	// Handshake
	TAuthChallenge = "auth.challenge" // server -> agent
	TAuthResponse  = "auth.response"  // agent  -> server
	TAuthOK        = "auth.ok"        // server -> agent
	TAuthFail      = "auth.fail"      // server -> agent

	// Telemetry (agent -> server)
	THeartbeat = "heartbeat"
	TInventory = "inventory"
	// TInventoryRequest asks the agent to send a full inventory of the
	// given kind (server -> agent), e.g. when the server's copy is missing.
	TInventoryRequest = "inventory.request"
	THealth           = "health"
	TBattery          = "battery"

	// Commands
	TCommand       = "command"        // server -> agent (signed)
	TCommandAck    = "command.ack"    // agent  -> server
	TCommandResult = "command.result" // agent  -> server
	// TCommandResultAck confirms a stored result (ref = the result's
	// envelope id); until then the agent keeps the result and resends it.
	TCommandResultAck = "command.result.ack" // server -> agent

	// Chat
	TChat     = "chat"      // both directions
	TChatRead = "chat.read" // both directions
	TChatAck  = "chat.ack"  // agent -> server: messages shown to the user

	// Remote session
	TRemoteRequest = "remote.request" // server -> agent (signed)
	TRemoteConsent = "remote.consent" // agent  -> server
	TRemoteStop    = "remote.stop"    // both directions
	TRemoteFrame   = "remote.frame"   // agent  -> server (binary frames use a separate channel)
	TRemoteInput   = "remote.input"   // server -> agent

	// Configuration
	TConfig = "config" // server -> agent
	// TConfigRequest asks the server to send the current configuration
	// (agent -> server); the reply is a TConfig with ref = the request id.
	// Agents send it periodically to check for updates. Servers that do not
	// know it ignore it.
	TConfigRequest = "config.request"

	// Keepalive
	TPing = "ping"
	TPong = "pong"
)

// Envelope is the framing for every message.
type Envelope struct {
	T   string          `json:"t"`
	ID  string          `json:"id,omitempty"`
	Ref string          `json:"ref,omitempty"`
	P   json.RawMessage `json:"p,omitempty"`
}

// NewEnvelope marshals payload into an envelope.
func NewEnvelope(t, id, ref string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = b
	}
	return Envelope{T: t, ID: id, Ref: ref, P: raw}, nil
}

// Decode unmarshals the payload into v.
func (e Envelope) Decode(v any) error {
	if len(e.P) == 0 {
		return nil
	}
	return json.Unmarshal(e.P, v)
}

// ---- Enrollment (HTTP) ----

type EnrollRequest struct {
	Token        string `json:"token"`
	MachineID    string `json:"machine_id"`
	Hostname     string `json:"hostname"`
	Domain       string `json:"domain,omitempty"`
	OS           string `json:"os"`
	OSVersion    string `json:"os_version"`
	AgentVersion string `json:"agent_version"`
	PublicKey    []byte `json:"public_key"` // Ed25519 public key
}

type EnrollResponse struct {
	AgentID       string `json:"agent_id"`
	DeviceID      string `json:"device_id"`
	ServerSignKey []byte `json:"server_sign_key"` // Ed25519 public key used to verify commands
}

// ---- Handshake ----

type AuthChallenge struct {
	Nonce         []byte `json:"nonce"`
	ServerVersion string `json:"server_version"`
	Protocol      int    `json:"protocol"`
}

type AuthResponse struct {
	AgentID      string    `json:"agent_id"`
	Timestamp    time.Time `json:"ts"`
	Signature    []byte    `json:"sig"`
	AgentVersion string    `json:"agent_version"`
	Protocol     int       `json:"protocol"`
	Capabilities []string  `json:"capabilities,omitempty"`
}

type AuthOK struct {
	DeviceID string      `json:"device_id"`
	Config   AgentConfig `json:"config"`
}

type AuthFail struct {
	Reason string `json:"reason"`
	// Reenroll tells the agent its identity is unknown and it should
	// discard its credentials and enroll again (if it has a token).
	Reenroll bool `json:"reenroll,omitempty"`
}

// AgentConfig is pushed by the server on connect and whenever it changes.
type AgentConfig struct {
	HeartbeatSec    int    `json:"heartbeat_sec"`
	HealthSec       int    `json:"health_sec"`
	BatterySec      int    `json:"battery_sec"`
	HardwareSec     int    `json:"hardware_sec"`
	SoftwareSec     int    `json:"software_sec"`
	ConsentPolicy   string `json:"consent_policy"` // always_ask | notify_only | deny
	ConsentTimeout  int    `json:"consent_timeout_sec"`
	RebootCountdown int    `json:"reboot_countdown_sec"`
	RebootMaxDefer  int    `json:"reboot_max_defer"`
	// UpdateCheckSec is how often a connected agent asks for the current
	// configuration (TConfigRequest) to pick up a new LatestAgent even when
	// a push was missed. Older agents ignore it.
	UpdateCheckSec int `json:"update_check_sec,omitempty"`
	// LatestAgent is the agent version the server wants running; builds
	// are keyed by "GOOS/GOARCH". Agents older than LatestAgent update
	// themselves from the matching build.
	LatestAgent string                `json:"latest_agent,omitempty"`
	AgentBuilds map[string]AgentBuild `json:"agent_builds,omitempty"`
}

// AgentBuild locates one downloadable agent binary.
type AgentBuild struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// DefaultAgentConfig holds the defaults described in docs/architecture.md.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		HeartbeatSec:    60,
		HealthSec:       15 * 60,
		BatterySec:      6 * 3600,
		HardwareSec:     24 * 3600,
		SoftwareSec:     6 * 3600,
		ConsentPolicy:   ConsentAlwaysAsk,
		ConsentTimeout:  60,
		RebootCountdown: 300,
		RebootMaxDefer:  3,
		UpdateCheckSec:  6 * 3600,
	}
}

// ConfigRequest is the payload of TConfigRequest.
type ConfigRequest struct {
	Reason       string `json:"reason,omitempty"` // update_check
	AgentVersion string `json:"agent_version"`
}

const (
	ConsentAlwaysAsk  = "always_ask"
	ConsentNotifyOnly = "notify_only"
	ConsentDeny       = "deny"
)

// ---- Telemetry ----

type Heartbeat struct {
	Time         time.Time `json:"time"`
	UptimeSec    uint64    `json:"uptime_sec"`
	CPUPercent   float64   `json:"cpu_percent"`
	MemTotal     uint64    `json:"mem_total"`
	MemUsed      uint64    `json:"mem_used"`
	LoggedInUser string    `json:"logged_in_user,omitempty"`
	SessionState string    `json:"session_state,omitempty"` // active | locked | none
	IPAddresses  []string  `json:"ip_addresses,omitempty"`
	AgentVersion string    `json:"agent_version"`
	HelperOnline bool      `json:"helper_online"`
	OnBattery    bool      `json:"on_battery,omitempty"`
	BatteryPct   int       `json:"battery_pct,omitempty"` // -1 when no battery
}

// Inventory is sent in full when Hash changes; otherwise the agent sends
// only {kind, hash, unchanged:true}.
type Inventory struct {
	Kind      string     `json:"kind"` // hardware | software
	Hash      string     `json:"hash"`
	Unchanged bool       `json:"unchanged,omitempty"`
	Collected time.Time  `json:"collected"`
	Hardware  *Hardware  `json:"hardware,omitempty"`
	Software  []Software `json:"software,omitempty"`
}

type Hardware struct {
	Manufacturer  string      `json:"manufacturer"`
	Model         string      `json:"model"`
	SerialNumber  string      `json:"serial_number"`
	ChassisType   string      `json:"chassis_type"` // desktop | laptop | server | vm | other
	BIOSVendor    string      `json:"bios_vendor"`
	BIOSVersion   string      `json:"bios_version"`
	BIOSDate      string      `json:"bios_date"`
	OSName        string      `json:"os_name"`
	OSVersion     string      `json:"os_version"`
	OSBuild       string      `json:"os_build"`
	OSEdition     string      `json:"os_edition"` // Home | Pro | Enterprise ...
	OSInstallDate string      `json:"os_install_date"`
	OSArch        string      `json:"os_arch"`
	Domain        string      `json:"domain"`
	DomainJoined  bool        `json:"domain_joined"`
	CPUs          []CPU       `json:"cpus"`
	MemoryTotal   uint64      `json:"memory_total"`
	MemoryModules []MemModule `json:"memory_modules"`
	Disks         []Disk      `json:"disks"`
	Volumes       []Volume    `json:"volumes"`
	NICs          []NIC       `json:"nics"`
	GPUs          []GPU       `json:"gpus"`
	Monitors      []Monitor   `json:"monitors"`
	TPMPresent    bool        `json:"tpm_present"`
	TPMVersion    string      `json:"tpm_version"`
	SecureBoot    bool        `json:"secure_boot"`
	BitLocker     []BitLocker `json:"bitlocker,omitempty"`
}

type CPU struct {
	Name         string `json:"name"`
	Cores        int    `json:"cores"`
	LogicalProcs int    `json:"logical_processors"`
	MaxClockMHz  int    `json:"max_clock_mhz"`
}

type MemModule struct {
	Slot         string `json:"slot"`
	Capacity     uint64 `json:"capacity"`
	SpeedMHz     int    `json:"speed_mhz"`
	Manufacturer string `json:"manufacturer"`
	PartNumber   string `json:"part_number"`
}

type Disk struct {
	Model        string `json:"model"`
	SerialNumber string `json:"serial_number"`
	Size         uint64 `json:"size"`
	MediaType    string `json:"media_type"` // SSD | HDD | Unspecified
	BusType      string `json:"bus_type"`   // NVMe | SATA | USB ...
	Health       string `json:"health"`     // Healthy | Warning | Unhealthy | Unknown
}

type Volume struct {
	Mount      string `json:"mount"`
	Label      string `json:"label"`
	FileSystem string `json:"file_system"`
	Size       uint64 `json:"size"`
	Free       uint64 `json:"free"`
}

type NIC struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	IPs       []string `json:"ips"`
	Gateway   []string `json:"gateway,omitempty"`
	DNS       []string `json:"dns,omitempty"`
	DHCP      bool     `json:"dhcp"`
	SpeedMbps uint64   `json:"speed_mbps"`
	Type      string   `json:"type"` // ethernet | wifi | other
}

type GPU struct {
	Name          string `json:"name"`
	DriverVersion string `json:"driver_version"`
	VRAM          uint64 `json:"vram"`
}

type Monitor struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	SerialNumber string `json:"serial_number"`
}

type BitLocker struct {
	Mount            string `json:"mount"`
	ProtectionStatus string `json:"protection_status"` // On | Off | Unknown
}

type Software struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	InstallDate string `json:"install_date,omitempty"`
	Arch        string `json:"arch,omitempty"`  // x64 | x86
	Scope       string `json:"scope,omitempty"` // machine | user
	UninstallID string `json:"uninstall_id,omitempty"`
}

// Health is a periodic diagnostic snapshot.
type Health struct {
	Collected         time.Time     `json:"collected"`
	CPUPercent        float64       `json:"cpu_percent"`
	MemPercent        float64       `json:"mem_percent"`
	UptimeSec         uint64        `json:"uptime_sec"`
	PendingReboot     bool          `json:"pending_reboot"`
	PendingRebootWhy  []string      `json:"pending_reboot_reasons,omitempty"`
	LastBoot          time.Time     `json:"last_boot"`
	Volumes           []Volume      `json:"volumes"`
	DiskHealth        []Disk        `json:"disk_health"`
	DefenderEnabled   *bool         `json:"defender_enabled,omitempty"`
	DefenderRealtime  *bool         `json:"defender_realtime,omitempty"`
	DefenderSigAgeDay *int          `json:"defender_sig_age_days,omitempty"`
	FirewallEnabled   *bool         `json:"firewall_enabled,omitempty"`
	LastUpdateInstall *time.Time    `json:"last_update_install,omitempty"`
	EventErrors24h    int           `json:"event_errors_24h"`
	TopEvents         []EventSample `json:"top_events,omitempty"`
	StoppedServices   []string      `json:"stopped_auto_services,omitempty"`
	TopProcesses      []Process     `json:"top_processes,omitempty"`
	Issues            []Issue       `json:"issues,omitempty"` // computed by the agent
}

type EventSample struct {
	Log      string    `json:"log"`
	Source   string    `json:"source"`
	EventID  int       `json:"event_id"`
	Count    int       `json:"count"`
	LastTime time.Time `json:"last_time"`
	Message  string    `json:"message"`
}

type Process struct {
	Name     string  `json:"name"`
	PID      int     `json:"pid"`
	CPU      float64 `json:"cpu"`
	MemBytes uint64  `json:"mem_bytes"`
}

// Issue severities.
const (
	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type Battery struct {
	Collected         time.Time `json:"collected"`
	Present           bool      `json:"present"`
	Name              string    `json:"name,omitempty"`
	Manufacturer      string    `json:"manufacturer,omitempty"`
	SerialNumber      string    `json:"serial_number,omitempty"`
	Chemistry         string    `json:"chemistry,omitempty"`
	DesignCapacity    uint32    `json:"design_capacity_mwh,omitempty"`
	FullChargeCap     uint32    `json:"full_charge_capacity_mwh,omitempty"`
	CycleCount        uint32    `json:"cycle_count,omitempty"`
	HealthPercent     float64   `json:"health_percent,omitempty"`
	ChargePercent     int       `json:"charge_percent,omitempty"`
	OnAC              bool      `json:"on_ac"`
	EstimatedRuntimeM int       `json:"estimated_runtime_min,omitempty"`
}

// ---- Commands ----

// Command kinds.
// MaxRebootDeferMinutes is the longest single deferral a user can choose
// for a reboot or shutdown; the server budgets command timeouts with it.
const MaxRebootDeferMinutes = 60

const (
	CmdPowerShell     = "powershell"
	CmdCmd            = "cmd"
	CmdReboot         = "reboot"
	CmdShutdown       = "shutdown"
	CmdRefresh        = "refresh"   // re-collect inventory/health/battery now
	CmdInstall        = "install"   // software deployment
	CmdUninstall      = "uninstall" // uninstall by UninstallID
	CmdAgentUpdate    = "agent.update"
	CmdNotify         = "notify" // show a notification to the user
	CmdLock           = "lock"   // lock the workstation
	CmdLogoff         = "logoff"
	CmdServiceRestart = "service.restart"
	CmdProcessKill    = "process.kill"
	CmdScan           = "discovery.scan" // agent acts as discovery probe
)

// Command is the signed payload of a TCommand message.
type Command struct {
	CommandID  string          `json:"command_id"`
	AgentID    string          `json:"agent_id"`
	Kind       string          `json:"kind"`
	Args       json.RawMessage `json:"args,omitempty"`
	TimeoutSec int             `json:"timeout_sec"`
	IssuedAt   time.Time       `json:"issued_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
	IssuedBy   string          `json:"issued_by"`
}

// SignedCommand wraps the canonical JSON bytes of a Command with a signature.
// The agent verifies Signature over Payload, then decodes Payload.
type SignedCommand struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"sig"`
}

type ScriptArgs struct {
	Script string            `json:"script"`
	Env    map[string]string `json:"env,omitempty"`
	// RunAs: system (default) | user (runs in the logged-in user session via helper)
	RunAs string `json:"run_as,omitempty"`
}

type RebootArgs struct {
	Force        bool   `json:"force"`
	CountdownSec int    `json:"countdown_sec"`
	Message      string `json:"message,omitempty"`
	AllowDefer   bool   `json:"allow_defer"`
	Shutdown     bool   `json:"shutdown,omitempty"`
}

type InstallArgs struct {
	PackageID    string `json:"package_id"`
	FileName     string `json:"file_name"`
	DownloadURL  string `json:"download_url"` // relative to server base, includes one-time token
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Type         string `json:"type"` // msi | exe | msix | ps1
	Arguments    string `json:"arguments"`
	SuccessCodes []int  `json:"success_codes,omitempty"` // default {0, 3010, 1641}
	DeploymentID string `json:"deployment_id,omitempty"`
}

type UninstallArgs struct {
	UninstallID string `json:"uninstall_id"`
	Name        string `json:"name"`
	Arguments   string `json:"arguments,omitempty"`
}

type NotifyArgs struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Level   string `json:"level,omitempty"` // info | warning | critical
}

type ServiceArgs struct {
	Name string `json:"name"`
}

type ProcessArgs struct {
	PID  int    `json:"pid,omitempty"`
	Name string `json:"name,omitempty"`
}

// AgentUpdateArgs carries a build per "GOOS/GOARCH"; the agent picks its own.
type AgentUpdateArgs struct {
	Version string                `json:"version"`
	Builds  map[string]AgentBuild `json:"builds,omitempty"`
}

type ScanArgs struct {
	CIDRs   []string `json:"cidrs"`
	Ports   []int    `json:"ports,omitempty"`
	ScanID  string   `json:"scan_id"`
	RatePPS int      `json:"rate_pps,omitempty"`
}

// Command status values.
const (
	StatusQueued    = "queued"
	StatusSent      = "sent"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusTimeout   = "timeout"
	StatusCanceled  = "canceled"
	StatusExpired   = "expired"
	StatusRejected  = "rejected" // agent refused (bad signature, user denied...)
)

type CommandAck struct {
	CommandID string `json:"command_id"`
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason,omitempty"`
}

type CommandResult struct {
	CommandID  string          `json:"command_id"`
	Status     string          `json:"status"`
	ExitCode   int             `json:"exit_code"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Data       json.RawMessage `json:"data,omitempty"` // kind-specific structured result
}

// DiscoveredHost is returned (as a list in CommandResult.Data) by an agent
// discovery scan, and produced by the server's own scanner.
type DiscoveredHost struct {
	IP         string `json:"ip"`
	MAC        string `json:"mac,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	OpenPorts  []int  `json:"open_ports,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	DeviceType string `json:"device_type,omitempty"`
	RTTMillis  int    `json:"rtt_ms,omitempty"`
}

// ---- Chat ----

type Chat struct {
	MessageID string    `json:"message_id"`
	From      string    `json:"from"` // "mis" or "user"
	Sender    string    `json:"sender"`
	Body      string    `json:"body"`
	SentAt    time.Time `json:"sent_at"`
}

// InventoryRequest is the payload of TInventoryRequest.
type InventoryRequest struct {
	Kind string `json:"kind"`
}

type ChatRead struct {
	MessageIDs []string `json:"message_ids"`
}

// ---- Remote sessions ----

const (
	RemoteView    = "view"
	RemoteControl = "control"
)

// RemoteRequest is signed like a command (Command.Kind = "remote", Args = RemoteRequest).
type RemoteRequest struct {
	SessionID  string `json:"session_id"`
	Mode       string `json:"mode"` // view | control
	Requester  string `json:"requester"`
	Reason     string `json:"reason"`
	RelayURL   string `json:"relay_url"` // path on the server for the agent media channel
	RelayToken string `json:"relay_token"`
	Policy     string `json:"policy"` // consent policy in effect
	TimeoutSec int    `json:"timeout_sec"`
}

type RemoteConsent struct {
	SessionID string `json:"session_id"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"` // denied | timeout | no_user | policy | helper_unavailable
	User      string `json:"user,omitempty"`
}

type RemoteStop struct {
	SessionID string `json:"session_id"`
	By        string `json:"by"` // user | mis | system
	Reason    string `json:"reason,omitempty"`
}
