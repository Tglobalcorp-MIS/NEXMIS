package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

// Execution states recorded in executedLog.
const (
	execClaimed = "claimed" // accepted, waiting for a worker slot
	execStarted = "started" // running; side effects may have happened
	execDone    = "done"    // result written to the outbox
)

type execEntry struct {
	At    time.Time `json:"at"`
	State string    `json:"state"`
}

// executedLog remembers recently claimed command ids so a re-delivered
// command is not run twice. Each entry records how far execution got, so a
// restart can tell a command that never started (safe to run when the
// server re-delivers it) from one interrupted midway (reported as failed).
type executedLog struct {
	mu   sync.Mutex
	path string
	ids  map[string]execEntry
}

const executedKeep = 2000

func newExecutedLog(dir string) *executedLog {
	l := &executedLog{path: filepath.Join(dir, "executed.json"), ids: map[string]execEntry{}}
	if b, err := os.ReadFile(l.path); err == nil {
		if json.Unmarshal(b, &l.ids) != nil {
			// Older agents stored id -> time for finished commands.
			var old map[string]time.Time
			if json.Unmarshal(b, &old) == nil {
				for k, v := range old {
					l.ids[k] = execEntry{At: v, State: execDone}
				}
			}
		}
	}
	return l
}

// Recover runs once at startup: claims that never started are dropped so
// the server's re-delivery executes them, and the ids of commands that
// were running when the agent stopped are returned (and marked done) so
// the caller can report them as failed.
func (l *executedLog) Recover() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var interrupted []string
	for id, e := range l.ids {
		switch e.State {
		case execClaimed:
			delete(l.ids, id)
		case execStarted:
			interrupted = append(interrupted, id)
			l.ids[id] = execEntry{At: e.At, State: execDone}
		}
	}
	sort.Strings(interrupted)
	l.save()
	return interrupted
}

// Claim records id and reports true if it had not been seen before.
func (l *executedLog) Claim(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.ids[id]; ok {
		return false
	}
	l.ids[id] = execEntry{At: time.Now(), State: execClaimed}
	if len(l.ids) > executedKeep {
		type kv struct {
			id string
			e  execEntry
		}
		all := make([]kv, 0, len(l.ids))
		for k, v := range l.ids {
			all = append(all, kv{k, v})
		}
		// Unfinished entries are always kept; finished ones are trimmed
		// oldest first.
		sort.Slice(all, func(i, j int) bool {
			if (all[i].e.State == execDone) != (all[j].e.State == execDone) {
				return all[i].e.State != execDone
			}
			return all[i].e.At.After(all[j].e.At)
		})
		l.ids = map[string]execEntry{}
		for i, e := range all {
			if i >= executedKeep/2 && e.e.State == execDone {
				break
			}
			l.ids[e.id] = e.e
		}
	}
	l.save()
	return true
}

// Mark advances the state of a claimed id.
func (l *executedLog) Mark(id, state string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.ids[id]
	if !ok {
		return
	}
	e.State = state
	l.ids[id] = e
	l.save()
}

func (l *executedLog) save() {
	b, _ := json.Marshal(l.ids)
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, l.path)
	}
}

// outbox persists command results until the server has them, so results
// survive disconnects and the reboot a command itself triggered.
type outbox struct {
	mu       sync.Mutex
	dir      string
	inflight map[string]bool // entry names sent on this connection, awaiting ack
}

const outboxMax = 500

func newOutbox(dir string) *outbox {
	d := filepath.Join(dir, "outbox")
	_ = os.MkdirAll(d, 0o700)
	return &outbox{dir: d}
}

func (o *outbox) Put(r protocol.CommandResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	name := filepath.Join(o.dir, time.Now().UTC().Format("20060102T150405.000000000")+"-"+safeName(r.CommandID)+".json")
	_ = os.WriteFile(name, b, 0o600)
	// Bound the queue: drop the oldest entries if the server has been
	// unreachable for a very long time.
	entries, _ := os.ReadDir(o.dir)
	for i := 0; i < len(entries)-outboxMax; i++ {
		_ = os.Remove(filepath.Join(o.dir, entries[i].Name()))
	}
}

// Pending returns queued results in order with their file names.
func (o *outbox) Pending() ([]string, []protocol.CommandResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entries, _ := os.ReadDir(o.dir)
	var names []string
	var out []protocol.CommandResult
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(o.dir, e.Name()))
		if err != nil {
			continue
		}
		var r protocol.CommandResult
		if json.Unmarshal(b, &r) != nil {
			_ = os.Remove(filepath.Join(o.dir, e.Name()))
			continue
		}
		names = append(names, e.Name())
		out = append(out, r)
	}
	return names, out
}

func (o *outbox) Done(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	name = filepath.Base(name)
	delete(o.inflight, name)
	_ = os.Remove(filepath.Join(o.dir, name))
}

// InFlight reports whether the entry was sent on the current connection
// and awaits the server's ack.
func (o *outbox) InFlight(name string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inflight[name]
}

func (o *outbox) SetInFlight(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight == nil {
		o.inflight = map[string]bool{}
	}
	o.inflight[name] = true
}

func (o *outbox) ResetInFlight() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inflight = nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}
