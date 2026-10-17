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

type execEntry struct {
	At   time.Time `json:"at"`
	Done bool      `json:"done"` // result written to the outbox
}

// executedLog remembers recently claimed command ids so a re-delivered
// (or replayed) signed command is never run twice. Entries not yet done
// when the agent stops are reported as failed on the next start: the
// server already holds them as running and will not deliver them again.
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
					l.ids[k] = execEntry{At: v, Done: true}
				}
			}
		}
	}
	return l
}

// Recover runs once at startup and returns (and marks done) the commands
// that were claimed but had no result when the agent stopped.
func (l *executedLog) Recover() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var unfinished []string
	for id, e := range l.ids {
		if !e.Done {
			unfinished = append(unfinished, id)
			l.ids[id] = execEntry{At: e.At, Done: true}
		}
	}
	sort.Strings(unfinished)
	l.save()
	return unfinished
}

// Claim records id and reports true if it had not been seen before.
func (l *executedLog) Claim(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.ids[id]; ok {
		return false
	}
	l.ids[id] = execEntry{At: time.Now()}
	if len(l.ids) > executedKeep {
		type kv struct {
			id string
			e  execEntry
		}
		all := make([]kv, 0, len(l.ids))
		for k, v := range l.ids {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].e.At.After(all[j].e.At) })
		l.ids = map[string]execEntry{}
		for _, e := range all[:executedKeep/2] {
			l.ids[e.id] = e.e
		}
	}
	l.save()
	return true
}

// Done records that the result of id is in the outbox.
func (l *executedLog) Done(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.ids[id]; ok {
		e.Done = true
		l.ids[id] = e
		l.save()
	}
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
