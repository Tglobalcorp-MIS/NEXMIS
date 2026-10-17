// Package helperipc defines the local protocol between the agent service
// (running as LocalSystem in session 0) and the user-session helper
// process (nexmis-agent.exe --user-helper). Messages are newline-delimited
// JSON envelopes over a named pipe (Windows) or a Unix socket (dev).
package helperipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Message types. Direction is noted as S->H (service to helper) or H->S.
const (
	THello = "hello" // H->S, first message: Hello
	TPing  = "ping"  // both
	TPong  = "pong"  // both

	TConsentRequest = "consent.request" // S->H: ConsentRequest
	TConsentResult  = "consent.result"  // H->S: ConsentResult

	TRemoteStart = "remote.start" // S->H: RemoteStart (helper opens the media channel)
	TRemoteStop  = "remote.stop"  // S->H: RemoteStop
	TRemoteEnded = "remote.ended" // H->S: RemoteEnded (user pressed "disconnect", error, ...)

	TChatMessage = "chat.message" // S->H: ChatMessage (from MIS)
	TChatSend    = "chat.send"    // H->S: ChatSend (from the user)
	TChatAck     = "chat.ack"     // H->S: IDs shown to the user
	TChatRead    = "chat.read"    // H->S: IDs the user has read
	TChatSeen    = "chat.seen"    // S->H: IDs of user messages MIS has read

	TNotify = "notify" // S->H: Notify

	TRebootPrompt   = "reboot.prompt"   // S->H: RebootPrompt
	TRebootResponse = "reboot.response" // H->S: RebootResponse

	TRunScript = "run.script" // S->H: RunScript (run as the logged-in user)
	TRunResult = "run.result" // H->S: RunResult

	TLock = "lock" // S->H: lock the workstation
	TSAS  = "sas"  // H->S: viewer requested Ctrl+Alt+Del (only the service can call SendSAS)

	TStatus = "status" // S->H: Status (agent connection state for the tray)
)

// Envelope frames every message.
type Envelope struct {
	T  string          `json:"t"`
	ID string          `json:"id,omitempty"`
	P  json.RawMessage `json:"p,omitempty"`
}

func (e Envelope) Decode(v any) error {
	if len(e.P) == 0 {
		return nil
	}
	return json.Unmarshal(e.P, v)
}

type Hello struct {
	User      string `json:"user"`       // DOMAIN\user
	SessionID uint32 `json:"session_id"` // Windows session id
	PID       int    `json:"pid"`
	Version   string `json:"version"`
}

type ConsentRequest struct {
	SessionID  string `json:"session_id"`
	Mode       string `json:"mode"` // view | control
	Requester  string `json:"requester"`
	Reason     string `json:"reason"`
	TimeoutSec int    `json:"timeout_sec"`
	// Policy notify_only means: do not ask, just show that a session starts.
	Policy string `json:"policy"`
}

type ConsentResult struct {
	SessionID string `json:"session_id"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"` // denied | timeout
	User      string `json:"user,omitempty"`
}

// RemoteStart tells the helper to connect to the relay and stream.
type RemoteStart struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Requester string `json:"requester"`
	URL       string `json:"url"`   // absolute wss:// URL of the relay endpoint
	Token     string `json:"token"` // relay bearer token
	TLS       TLS    `json:"tls"`
}

// TLS carries the server-trust settings the service uses so the helper
// validates the server exactly the same way.
type TLS struct {
	CAPEM              string `json:"ca_pem,omitempty"`
	PinSHA256          string `json:"pin_sha256,omitempty"` // hex SHA-256 of the server leaf certificate
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

type RemoteStop struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

type RemoteEnded struct {
	SessionID string `json:"session_id"`
	By        string `json:"by"` // user | error
	Reason    string `json:"reason,omitempty"`
}

type ChatMessage struct {
	MessageID string    `json:"message_id"`
	Sender    string    `json:"sender"`
	Body      string    `json:"body"`
	SentAt    time.Time `json:"sent_at"`
	FromUser  bool      `json:"from_user,omitempty"` // history echo of the user's own messages
}

type ChatSend struct {
	Body string `json:"body"`
	User string `json:"user,omitempty"`
}

type IDs struct {
	IDs []string `json:"ids"`
}

type Notify struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Level   string `json:"level,omitempty"` // info | warning | critical
}

type RebootPrompt struct {
	ID           string `json:"id"`
	CountdownSec int    `json:"countdown_sec"`
	Message      string `json:"message,omitempty"`
	AllowDefer   bool   `json:"allow_defer"`
	DefersLeft   int    `json:"defers_left"`
	Shutdown     bool   `json:"shutdown,omitempty"`
	Requester    string `json:"requester,omitempty"`
}

// Reboot actions.
const (
	RebootNow     = "now"
	RebootDefer   = "defer"
	RebootTimeout = "timeout"
)

type RebootResponse struct {
	ID           string `json:"id"`
	Action       string `json:"action"`
	DeferMinutes int    `json:"defer_minutes,omitempty"`
}

type RunScript struct {
	ID         string            `json:"id"`
	Shell      string            `json:"shell"` // powershell | cmd
	Script     string            `json:"script"`
	TimeoutSec int               `json:"timeout_sec"`
	Env        map[string]string `json:"env,omitempty"`
}

type RunResult struct {
	ID        string `json:"id"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

type Status struct {
	Connected  bool   `json:"connected"`
	ServerURL  string `json:"server_url"`
	DeviceName string `json:"device_name"`
	Version    string `json:"version"`
}

// MaxLine bounds a single message (scripts and results can be large).
const MaxLine = 4 << 20

// Conn is a framed, concurrency-safe message connection.
type Conn struct {
	rw   io.ReadWriteCloser
	r    *bufio.Reader
	wmu  sync.Mutex
	once sync.Once
}

func NewConn(rw io.ReadWriteCloser) *Conn {
	return &Conn{rw: rw, r: bufio.NewReaderSize(rw, 64<<10)}
}

// ErrTooLarge is returned for an oversized line.
var ErrTooLarge = errors.New("helperipc: message too large")

// Send writes one message.
func (c *Conn) Send(t, id string, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	b, err := json.Marshal(Envelope{T: t, ID: id, P: raw})
	if err != nil {
		return err
	}
	if len(b) > MaxLine {
		return ErrTooLarge
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.rw.Write(b)
	return err
}

// Recv reads one message.
func (c *Conn) Recv() (Envelope, error) {
	var line []byte
	for {
		chunk, isPrefix, err := c.r.ReadLine()
		if err != nil {
			return Envelope{}, err
		}
		line = append(line, chunk...)
		if len(line) > MaxLine {
			return Envelope{}, ErrTooLarge
		}
		if !isPrefix {
			break
		}
	}
	var e Envelope
	err := json.Unmarshal(line, &e)
	return e, err
}

func (c *Conn) Close() error {
	var err error
	c.once.Do(func() { err = c.rw.Close() })
	return err
}
