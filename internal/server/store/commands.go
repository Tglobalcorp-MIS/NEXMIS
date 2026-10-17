package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

type Command struct {
	ID           uuid.UUID       `json:"id"`
	DeviceID     uuid.UUID       `json:"device_id"`
	DeviceName   string          `json:"device_name"`
	Kind         string          `json:"kind"`
	Args         json.RawMessage `json:"args"`
	Status       string          `json:"status"`
	TimeoutSec   int             `json:"timeout_sec"`
	ExitCode     *int            `json:"exit_code"`
	Stdout       string          `json:"stdout"`
	Stderr       string          `json:"stderr"`
	Truncated    bool            `json:"truncated"`
	Data         json.RawMessage `json:"data,omitempty"`
	IssuedBy     *uuid.UUID      `json:"-"`
	IssuedByName string          `json:"issued_by_name"`
	CreatedAt    time.Time       `json:"created_at"`
	SentAt       *time.Time      `json:"sent_at"`
	StartedAt    *time.Time      `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
	DeploymentID *uuid.UUID      `json:"deployment_id"`
	ScheduleID   *uuid.UUID      `json:"schedule_id"`
}

const commandCols = `c.id, c.device_id, d.name, c.kind, c.args, c.status, c.timeout_sec, c.exit_code, c.stdout, c.stderr,
	c.truncated, c.data, c.issued_by, c.issued_by_name, c.created_at, c.sent_at, c.started_at, c.finished_at,
	c.expires_at, c.deployment_id, c.schedule_id`

const commandFrom = ` FROM commands c JOIN devices d ON d.id = c.device_id`

func scanCommand(row pgx.Row) (*Command, error) {
	var c Command
	var args, data []byte
	err := row.Scan(&c.ID, &c.DeviceID, &c.DeviceName, &c.Kind, &args, &c.Status, &c.TimeoutSec, &c.ExitCode,
		&c.Stdout, &c.Stderr, &c.Truncated, &data, &c.IssuedBy, &c.IssuedByName, &c.CreatedAt, &c.SentAt,
		&c.StartedAt, &c.FinishedAt, &c.ExpiresAt, &c.DeploymentID, &c.ScheduleID)
	if err != nil {
		return nil, mapErr(err)
	}
	c.Args = json.RawMessage(args)
	if len(data) > 0 {
		c.Data = json.RawMessage(data)
	}
	return &c, nil
}

// NewCommandRow is the input for queuing a command.
type NewCommandRow struct {
	DeviceID     uuid.UUID
	Kind         string
	Args         json.RawMessage
	TimeoutSec   int
	TTL          time.Duration // how long it may wait for an offline device
	IssuedBy     *uuid.UUID
	IssuedByName string
	DeploymentID *uuid.UUID
	ScheduleID   *uuid.UUID
}

func (s *Store) CreateCommand(ctx context.Context, q Querier, in NewCommandRow) (*Command, error) {
	if q == nil {
		q = s.Pool
	}
	if len(in.Args) == 0 {
		in.Args = json.RawMessage(`{}`)
	}
	if in.TTL <= 0 {
		in.TTL = 24 * time.Hour
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `INSERT INTO commands (device_id, kind, args, status, timeout_sec, issued_by, issued_by_name,
		expires_at, deployment_id, schedule_id) VALUES ($1,$2,$3,'queued',$4,$5,$6,$7,$8,$9) RETURNING id`,
		in.DeviceID, in.Kind, []byte(in.Args), in.TimeoutSec, in.IssuedBy, in.IssuedByName, time.Now().Add(in.TTL),
		in.DeploymentID, in.ScheduleID).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return scanCommand(q.QueryRow(ctx, `SELECT `+commandCols+commandFrom+` WHERE c.id = $1`, id))
}

func (s *Store) GetCommand(ctx context.Context, id uuid.UUID) (*Command, error) {
	return scanCommand(s.Pool.QueryRow(ctx, `SELECT `+commandCols+commandFrom+` WHERE c.id = $1`, id))
}

type CommandFilter struct {
	DeviceID     *uuid.UUID
	DeploymentID *uuid.UUID
	Status       string
	Kind         string
}

func (s *Store) ListCommands(ctx context.Context, f CommandFilter, p Page) ([]Command, int, error) {
	w := &whereBuilder{}
	if f.DeviceID != nil {
		w.add("c.device_id = ?", *f.DeviceID)
	}
	if f.DeploymentID != nil {
		w.add("c.deployment_id = ?", *f.DeploymentID)
	}
	if f.Status != "" {
		w.add("c.status = ?", f.Status)
	}
	if f.Kind != "" {
		w.add("c.kind = ?", f.Kind)
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM commands c`+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT `+commandCols+commandFrom+w.sql()+
		fmt.Sprintf(` ORDER BY c.created_at DESC LIMIT %d OFFSET %d`, limit, offset), w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

// PendingCommands returns queued (and sent-but-unacknowledged) commands for
// a device, oldest first, expiring stale ones on the way.
func (s *Store) PendingCommands(ctx context.Context, deviceID uuid.UUID) ([]Command, error) {
	if _, err := s.Pool.Exec(ctx, `UPDATE commands SET status = 'expired', finished_at = now()
		WHERE device_id = $1 AND status IN ('queued','sent') AND expires_at < now()`, deviceID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+commandCols+commandFrom+` WHERE c.device_id = $1 AND c.status IN ('queued','sent')
		ORDER BY c.created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) MarkCommandSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE commands SET status = 'sent', sent_at = now() WHERE id = $1 AND status IN ('queued','sent')`, id)
	return err
}

// ApplyAck records the agent's acceptance or rejection.
func (s *Store) ApplyAck(ctx context.Context, deviceID uuid.UUID, a protocol.CommandAck) (*Command, error) {
	id, err := uuid.Parse(a.CommandID)
	if err != nil {
		return nil, err
	}
	if a.Accepted {
		_, err = s.Pool.Exec(ctx, `UPDATE commands SET status = 'running', started_at = now()
			WHERE id = $1 AND device_id = $2 AND status IN ('queued','sent')`, id, deviceID)
	} else {
		_, err = s.Pool.Exec(ctx, `UPDATE commands SET status = 'rejected', stderr = $3, finished_at = now()
			WHERE id = $1 AND device_id = $2 AND status IN ('queued','sent','running')`, id, deviceID, a.Reason)
	}
	if err != nil {
		return nil, err
	}
	return s.GetCommand(ctx, id)
}

// maxOutput caps stored stdout/stderr at 1 MiB each.
const maxOutput = 1 << 20

func (s *Store) ApplyResult(ctx context.Context, deviceID uuid.UUID, r protocol.CommandResult) (*Command, error) {
	id, err := uuid.Parse(r.CommandID)
	if err != nil {
		return nil, err
	}
	truncated := r.Truncated
	if len(r.Stdout) > maxOutput {
		r.Stdout, truncated = r.Stdout[:maxOutput], true
	}
	if len(r.Stderr) > maxOutput {
		r.Stderr, truncated = r.Stderr[:maxOutput], true
	}
	var data []byte
	if len(r.Data) > 0 {
		data = r.Data
	}
	started := r.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	finished := r.FinishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE commands SET status = $3, exit_code = $4, stdout = $5, stderr = $6, truncated = $7,
		data = $8, started_at = COALESCE(started_at, $9), finished_at = $10
		WHERE id = $1 AND device_id = $2 AND status NOT IN ('succeeded','failed','timeout','canceled','expired')`,
		id, deviceID, r.Status, r.ExitCode, toValidUTF8(r.Stdout), toValidUTF8(r.Stderr), truncated, data, started, finished)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetCommand(ctx, id)
}

func (s *Store) CancelCommand(ctx context.Context, id uuid.UUID) (*Command, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE commands SET status = 'canceled', finished_at = now()
		WHERE id = $1 AND status IN ('queued','sent')`, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrConflict
	}
	return s.GetCommand(ctx, id)
}

// FailOrphanedCommands marks commands that were running on a device when it
// disconnected for longer than their timeout as timed out.
func (s *Store) TimeoutStaleCommands(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE commands SET status = 'timeout', finished_at = now(),
		stderr = CASE WHEN stderr = '' THEN '執行逾時，未收到 Agent 回報結果' ELSE stderr END
		WHERE status = 'running' AND started_at < now() - make_interval(secs => timeout_sec + 600)
		RETURNING id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ---- scripts ----

type Script struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Shell       string    `json:"shell"`
	Body        string    `json:"body"`
	TimeoutSec  int       `json:"timeout_sec"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, description, shell, body, timeout_sec, created_at, updated_at
		FROM scripts ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Script, error) {
		var sc Script
		err := r.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Shell, &sc.Body, &sc.TimeoutSec, &sc.CreatedAt, &sc.UpdatedAt)
		return sc, err
	})
}

func (s *Store) CreateScript(ctx context.Context, sc *Script, by uuid.UUID) error {
	return mapErr(s.Pool.QueryRow(ctx, `INSERT INTO scripts (name, description, shell, body, timeout_sec, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at, updated_at`,
		sc.Name, sc.Description, sc.Shell, sc.Body, sc.TimeoutSec, by).Scan(&sc.ID, &sc.CreatedAt, &sc.UpdatedAt))
}

type ScriptPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Shell       *string `json:"shell"`
	Body        *string `json:"body"`
	TimeoutSec  *int    `json:"timeout_sec"`
}

func (s *Store) UpdateScript(ctx context.Context, id uuid.UUID, p ScriptPatch) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE scripts SET name = COALESCE($2, name), description = COALESCE($3, description),
		shell = COALESCE($4, shell), body = COALESCE($5, body), timeout_sec = COALESCE($6, timeout_sec), updated_at = now()
		WHERE id = $1`, id, p.Name, p.Description, p.Shell, p.Body, p.TimeoutSec)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteScript(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM scripts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- chat ----

type ChatMessage struct {
	ID          uuid.UUID  `json:"id"`
	DeviceID    uuid.UUID  `json:"device_id"`
	Direction   string     `json:"direction"`
	Sender      string     `json:"sender"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	ReadAt      *time.Time `json:"read_at"`
}

const chatCols = `id, device_id, direction, sender, body, created_at, delivered_at, read_at`

func scanChat(r pgx.CollectableRow) (ChatMessage, error) {
	var m ChatMessage
	err := r.Scan(&m.ID, &m.DeviceID, &m.Direction, &m.Sender, &m.Body, &m.CreatedAt, &m.DeliveredAt, &m.ReadAt)
	return m, err
}

func (s *Store) CreateChat(ctx context.Context, deviceID uuid.UUID, direction, sender, body string) (*ChatMessage, error) {
	rows, err := s.Pool.Query(ctx, `INSERT INTO chat_messages (device_id, direction, sender, body,
		delivered_at) VALUES ($1,$2,$3,$4, CASE WHEN $2 = 'user' THEN now() END) RETURNING `+chatCols,
		deviceID, direction, sender, body)
	if err != nil {
		return nil, err
	}
	m, err := pgx.CollectExactlyOneRow(rows, scanChat)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

func (s *Store) ListChat(ctx context.Context, deviceID uuid.UUID, before *time.Time, limit int) ([]ChatMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT * FROM (SELECT `+chatCols+` FROM chat_messages WHERE device_id = $1
		AND ($2::timestamptz IS NULL OR created_at < $2) ORDER BY created_at DESC LIMIT $3) m ORDER BY created_at`,
		deviceID, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanChat)
}

// UndeliveredChat returns MIS messages that have not reached the device.
func (s *Store) UndeliveredChat(ctx context.Context, deviceID uuid.UUID) ([]ChatMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+chatCols+` FROM chat_messages WHERE device_id = $1 AND direction = 'mis'
		AND delivered_at IS NULL AND created_at > now() - interval '7 days' ORDER BY created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanChat)
}

func (s *Store) MarkChatDelivered(ctx context.Context, deviceID uuid.UUID, ids []uuid.UUID) ([]ChatMessage, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE chat_messages SET delivered_at = COALESCE(delivered_at, now())
		WHERE device_id = $1 AND id = ANY($2) RETURNING `+chatCols, deviceID, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanChat)
}

// MarkChatRead marks messages in one direction as read. ids==nil marks all.
func (s *Store) MarkChatRead(ctx context.Context, deviceID uuid.UUID, direction string, ids []uuid.UUID) ([]ChatMessage, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE chat_messages SET read_at = now(), delivered_at = COALESCE(delivered_at, now())
		WHERE device_id = $1 AND direction = $2 AND read_at IS NULL AND ($3::uuid[] IS NULL OR id = ANY($3))
		RETURNING `+chatCols, deviceID, direction, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanChat)
}

func (s *Store) UnreadChatCount(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE direction = 'user' AND read_at IS NULL`).Scan(&n)
	return n, err
}

// ---- remote sessions ----

type RemoteSession struct {
	ID            uuid.UUID  `json:"id"`
	DeviceID      uuid.UUID  `json:"device_id"`
	DeviceName    string     `json:"device_name"`
	Mode          string     `json:"mode"`
	Status        string     `json:"status"`
	RequestedBy   *uuid.UUID `json:"-"`
	RequesterName string     `json:"requester_name"`
	Reason        string     `json:"reason"`
	ConsentUser   string     `json:"consent_user"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
	EndReason     string     `json:"end_reason"`
}

const remoteCols = `r.id, r.device_id, d.name, r.mode, r.status, r.requested_by, r.requester_name, r.reason, r.consent_user,
	r.created_at, r.started_at, r.ended_at, r.end_reason`

func scanRemote(row pgx.Row) (*RemoteSession, error) {
	var r RemoteSession
	err := row.Scan(&r.ID, &r.DeviceID, &r.DeviceName, &r.Mode, &r.Status, &r.RequestedBy, &r.RequesterName, &r.Reason,
		&r.ConsentUser, &r.CreatedAt, &r.StartedAt, &r.EndedAt, &r.EndReason)
	if err != nil {
		return nil, mapErr(err)
	}
	return &r, nil
}

func (s *Store) CreateRemoteSession(ctx context.Context, deviceID uuid.UUID, mode string, by uuid.UUID, byName, reason string) (*RemoteSession, error) {
	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO remote_sessions (device_id, mode, status, requested_by, requester_name, reason)
		VALUES ($1,$2,'pending',$3,$4,$5) RETURNING id`, deviceID, mode, by, byName, reason).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return s.GetRemoteSession(ctx, id)
}

func (s *Store) GetRemoteSession(ctx context.Context, id uuid.UUID) (*RemoteSession, error) {
	return scanRemote(s.Pool.QueryRow(ctx, `SELECT `+remoteCols+` FROM remote_sessions r JOIN devices d ON d.id = r.device_id
		WHERE r.id = $1`, id))
}

func (s *Store) ListRemoteSessions(ctx context.Context, deviceID *uuid.UUID, p Page) ([]RemoteSession, int, error) {
	w := &whereBuilder{}
	if deviceID != nil {
		w.add("r.device_id = ?", *deviceID)
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM remote_sessions r`+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := p.limitOffset()
	rows, err := s.Pool.Query(ctx, `SELECT `+remoteCols+` FROM remote_sessions r JOIN devices d ON d.id = r.device_id`+w.sql()+
		fmt.Sprintf(` ORDER BY r.created_at DESC LIMIT %d OFFSET %d`, limit, offset), w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RemoteSession{}
	for rows.Next() {
		r, err := scanRemote(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	return out, total, rows.Err()
}

// UpdateRemoteStatus moves a session to a new status. Terminal statuses
// (denied, ended, failed) are never left.
func (s *Store) UpdateRemoteStatus(ctx context.Context, id uuid.UUID, status, consentUser, endReason string) (*RemoteSession, error) {
	_, err := s.Pool.Exec(ctx, `UPDATE remote_sessions SET status = $2,
		consent_user = CASE WHEN $3 <> '' THEN $3 ELSE consent_user END,
		started_at = CASE WHEN $2 = 'active' THEN now() ELSE started_at END,
		ended_at = CASE WHEN $2 IN ('denied','ended','failed') THEN now() ELSE ended_at END,
		end_reason = CASE WHEN $4 <> '' THEN $4 ELSE end_reason END
		WHERE id = $1 AND status IN ('pending','active')`, id, status, consentUser, endReason)
	if err != nil {
		return nil, err
	}
	return s.GetRemoteSession(ctx, id)
}

// EndStaleRemoteSessions closes sessions left open by a server restart.
func (s *Store) EndStaleRemoteSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE remote_sessions SET status = CASE WHEN status = 'pending' THEN 'failed' ELSE 'ended' END,
		ended_at = now(), end_reason = 'server_restart' WHERE status IN ('pending','active')`)
	return err
}
