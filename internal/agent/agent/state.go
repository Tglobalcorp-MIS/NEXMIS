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

// executedLog remembers recently executed command ids so a re-delivered
// (or replayed) signed command is never run twice.
type executedLog struct {
	mu   sync.Mutex
	path string
	ids  map[string]time.Time
}

const executedKeep = 2000

func newExecutedLog(dir string) *executedLog {
	l := &executedLog{path: filepath.Join(dir, "executed.json"), ids: map[string]time.Time{}}
	if b, err := os.ReadFile(l.path); err == nil {
		_ = json.Unmarshal(b, &l.ids)
	}
	return l
}

// Claim records id and reports true if it had not been seen before.
func (l *executedLog) Claim(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.ids[id]; ok {
		return false
	}
	l.ids[id] = time.Now()
	if len(l.ids) > executedKeep {
		type kv struct {
			id string
			t  time.Time
		}
		all := make([]kv, 0, len(l.ids))
		for k, v := range l.ids {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
		l.ids = map[string]time.Time{}
		for _, e := range all[:executedKeep/2] {
			l.ids[e.id] = e.t
		}
	}
	b, _ := json.Marshal(l.ids)
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, l.path)
	}
	return true
}

// outbox persists command results until the server has them, so results
// survive disconnects and the reboot a command itself triggered.
type outbox struct {
	mu  sync.Mutex
	dir string
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
	_ = os.Remove(filepath.Join(o.dir, filepath.Base(name)))
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}
