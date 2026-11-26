// Package winproc is the Windows process tracker: it cuts the network of
// target apps by putting their program paths in the jail (a WFP filter per
// path, see wfp.Jail).
//
// Differences from the Linux cgroup jail, all inherent to filtering by path:
//
//   - the block applies to every instance of the program, and stays while
//     the program is a target even if it is not running, so a relaunch is
//     offline from the first connection;
//   - "children inherit the block" is emulated: a program started by a
//     target (a game launched by Steam) is jailed while that parent runs.
//     Programs under the Windows directory (conhost, WerFault…) are never
//     jailed this way, since the filter would hit them system wide;
//   - connections opened before the jail are reset, but only IPv4 TCP:
//     Windows has no call to drop IPv6 or UDP flows of another process.
//
// The logic here is pure; the OS goes behind System and Jail.
package winproc

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
)

// Proc is what the tracker needs to know about a process.
type Proc struct {
	PID, PPID int
	Name      string // image name, e.g. "Discord.exe"
	Path      string // full image path; "" when unknown
	Created   int64  // creation time, any monotonic unit; 0 when unknown
}

// System is the process side of the OS.
type System interface {
	Procs() (map[int]Proc, error)
	// KillConns resets the connections the processes already had.
	KillConns(pids []int) error
}

// Jail is the set of program paths without network.
type Jail interface {
	// Paths lists the jailed programs. It may return paths and an error
	// together (some cleanup failed).
	Paths() ([]string, error)
	Add(path string) error
	Remove(path string) error
}

type Tracker struct {
	Sys      System
	Jail     Jail
	Interval time.Duration
	Log      *slog.Logger
	// Protected are lowercase directory prefixes whose programs are only
	// jailed when they are targets themselves, never as descendants.
	Protected []string

	mu      sync.Mutex
	targets map[string]bool // normalized names
	jailed  []backend.JailedProc
	kick    chan struct{}
}

var _ backend.ProcessTracker = (*Tracker)(nil)

func New(sys System, jail Jail, log *slog.Logger) *Tracker {
	t := &Tracker{Sys: sys, Jail: jail, Interval: time.Second, Log: log}
	if root := os.Getenv("SystemRoot"); root != "" {
		t.Protected = []string{strings.ToLower(strings.TrimRight(root, `\/`)) + `\`}
	}
	return t
}

func (t *Tracker) init() {
	if t.targets == nil {
		t.targets = map[string]bool{}
		t.kick = make(chan struct{}, 1)
	}
}

// Prepare has nothing to do: the jail is per program, created on demand.
func (t *Tracker) Prepare() error { return nil }

func (t *Tracker) SetTargets(names []string) {
	t.mu.Lock()
	t.init()
	next := map[string]bool{}
	for _, n := range names {
		if n = normName(n); n != "" {
			next[n] = true
		}
	}
	changed := len(next) != len(t.targets)
	for n := range next {
		changed = changed || !t.targets[n]
	}
	t.targets = next
	t.mu.Unlock()
	if changed {
		select {
		case t.kick <- struct{}{}:
		default:
		}
	}
}

func (t *Tracker) Jailed() []backend.JailedProc {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.jailed)
}

func (t *Tracker) Run(ctx context.Context) error {
	t.mu.Lock()
	t.init()
	t.mu.Unlock()
	tick := time.NewTicker(t.Interval)
	defer tick.Stop()
	for {
		if err := t.Scan(ctx); err != nil {
			t.Log.Error("winproc", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-t.kick:
		}
	}
}

// Restore releases every jailed program.
func (t *Tracker) Restore(ctx context.Context) error {
	t.SetTargets(nil)
	return t.Scan(ctx)
}

// Scan does one pass: release programs that are no longer wanted, jail the
// new ones and reset their open connections.
func (t *Tracker) Scan(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()

	procs, err := t.Sys.Procs()
	if err != nil {
		return err
	}
	jailedPaths, err := t.Jail.Paths()
	if err != nil {
		if jailedPaths == nil {
			return err
		}
		t.Log.Warn("winproc: limpeza parcial do jail", "err", err)
	}

	// want: lowercase path → path as reported.
	want := map[string]string{}
	self := os.Getpid()
	for pid, p := range procs {
		if pid <= 4 || pid == self || p.Path == "" {
			continue
		}
		if t.wanted(p, procs) {
			want[strings.ToLower(p.Path)] = p.Path
		}
	}
	// Targets stay jailed while not running, so a relaunch starts offline.
	for _, j := range jailedPaths {
		if t.targets[normName(baseName(j))] {
			want[strings.ToLower(j)] = j
		}
	}

	have := map[string]string{}
	for _, j := range jailedPaths {
		have[strings.ToLower(j)] = j
	}
	for k, j := range have {
		if _, ok := want[k]; ok {
			continue
		}
		if err := t.Jail.Remove(j); err != nil {
			t.Log.Warn("não consegui liberar programa", "path", j, "err", err)
			want[k] = j // still jailed
			continue
		}
		t.Log.Info("rede liberada", "path", j)
	}
	added := map[string]bool{}
	for k, p := range want {
		if _, ok := have[k]; ok {
			continue
		}
		if err := t.Jail.Add(p); err != nil {
			t.Log.Warn("não consegui isolar programa", "path", p, "err", err)
			delete(want, k)
			continue
		}
		added[k] = true
		t.Log.Info("rede cortada", "path", p)
	}

	var kill []int
	t.jailed = t.jailed[:0]
	for pid, p := range procs {
		k := strings.ToLower(p.Path)
		if _, ok := want[k]; !ok || p.Path == "" {
			continue
		}
		t.jailed = append(t.jailed, backend.JailedProc{PID: pid, Name: displayName(p)})
		if added[k] {
			kill = append(kill, pid)
		}
	}
	slices.SortFunc(t.jailed, func(a, b backend.JailedProc) int { return a.PID - b.PID })
	if len(kill) > 0 {
		slices.Sort(kill)
		if err := t.Sys.KillConns(kill); err != nil {
			t.Log.Warn("não consegui derrubar conexões abertas", "pids", kill, "err", err)
		}
	}
	return nil
}

// wanted: p is a target, or descends from a running target (64 levels at
// most). A parent created after its child is a reused PID, not the parent.
func (t *Tracker) wanted(p Proc, procs map[int]Proc) bool {
	if t.targeted(p) {
		return true
	}
	if t.protected(p.Path) {
		return false
	}
	cur := p
	for range 64 {
		parent, ok := procs[cur.PPID]
		if !ok || parent.PID == cur.PID || parent.PID <= 4 {
			return false
		}
		if parent.Created != 0 && cur.Created != 0 && parent.Created > cur.Created {
			return false
		}
		if t.targeted(parent) {
			return true
		}
		cur = parent
	}
	return false
}

func (t *Tracker) targeted(p Proc) bool {
	return t.targets[normName(baseName(p.Path))] || t.targets[normName(p.Name)]
}

func (t *Tracker) protected(path string) bool {
	lp := strings.ToLower(path)
	for _, pre := range t.Protected {
		if strings.HasPrefix(lp, pre) {
			return true
		}
	}
	return false
}

// normName makes catalog names and image names comparable: "Discord",
// "discord.exe" and `C:\…\Discord.exe` all become "discord".
func normName(s string) string {
	s = strings.ToLower(strings.TrimSpace(baseName(s)))
	return strings.TrimSuffix(s, ".exe")
}

// baseName splits on both separators, so it works on Windows paths anywhere.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func displayName(p Proc) string {
	if p.Name != "" {
		return p.Name
	}
	return baseName(p.Path)
}
