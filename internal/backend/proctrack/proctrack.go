// Package proctrack cuts the network of local apps: processes whose name is a
// target are moved into a dedicated cgroup (the jail) that the firewall
// rejects with `socket cgroupv2`. Children forked inside the jail inherit it.
//
// A socket's cgroup is fixed when it is created, so sockets opened before the
// move would keep working; those are destroyed with `ss -K` right after.
package proctrack

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

// Proc is what the tracker needs to know about a process.
type Proc struct {
	PID  int
	PPID int
	Comm string // /proc/pid/comm (truncated to 15 chars by the kernel)
	Exe  string // basename of /proc/pid/exe
}

// Conn is a connected socket (TCP or UDP) owned by a process.
type Conn struct {
	UDP      bool
	Src, Dst netip.AddrPort
}

// System is the OS surface the tracker uses; Linux implements it over
// /proc and /sys/fs/cgroup, tests use a fake.
type System interface {
	Procs() (map[int]Proc, error)
	// CgroupOf returns the cgroup path of pid relative to the cgroup2 root.
	CgroupOf(pid int) (string, error)
	CgroupProcs(cg string) ([]int, error)
	Move(pid int, cg string) error
	MkCgroup(cg string) error
	Conns(pid int) ([]Conn, error)
}

type Tracker struct {
	Sys      System
	Exec     sysexec.Runner // runs ss -K
	Jail     string         // e.g. "blockd.jail"
	Interval time.Duration
	Log      *slog.Logger

	mu      sync.Mutex
	targets map[string]bool
	origins map[int]string
	jailed  []backend.JailedProc
	kick    chan struct{}
}

var _ backend.ProcessTracker = (*Tracker)(nil)

func New(jail string, log *slog.Logger) *Tracker {
	return &Tracker{
		Sys: LinuxSystem{CgroupRoot: "/sys/fs/cgroup", ProcRoot: "/proc"}, Exec: sysexec.Exec{},
		Jail: jail, Interval: time.Second, Log: log,
	}
}

func (t *Tracker) init() {
	if t.origins == nil {
		t.origins = map[int]string{}
		t.targets = map[string]bool{}
		t.kick = make(chan struct{}, 1)
	}
}

func (t *Tracker) Prepare() error { return t.Sys.MkCgroup(t.Jail) }

func (t *Tracker) SetTargets(names []string) {
	t.mu.Lock()
	t.init()
	next := map[string]bool{}
	for _, n := range names {
		next[n] = true
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
			t.Log.Error("proctrack", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-t.kick:
		}
	}
}

// Restore releases every jailed process.
func (t *Tracker) Restore(ctx context.Context) error {
	t.SetTargets(nil)
	return t.Scan(ctx)
}

func (t *Tracker) targeted(p Proc) bool {
	return t.targets[p.Comm] || t.targets[p.Exe]
}

// Scan does one pass: release what is no longer targeted, jail new targets.
func (t *Tracker) Scan(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()

	procs, err := t.Sys.Procs()
	if err != nil {
		return err
	}
	inJail := map[int]bool{}
	if pids, err := t.Sys.CgroupProcs(t.Jail); err == nil {
		for _, pid := range pids {
			inJail[pid] = true
		}
	} else if len(t.targets) > 0 {
		return fmt.Errorf("jail %s: %w", t.Jail, err)
	}

	// Release first, so an emptied target list frees everything.
	// Origins are resolved before anything moves: a child looks up its parent's.
	release := map[int]string{}
	for pid := range inJail {
		if !t.keep(pid, procs, inJail) {
			release[pid] = t.originOf(pid, procs)
		}
	}
	for pid, origin := range release {
		t.release(pid, origin)
		delete(inJail, pid)
	}

	self := os.Getpid()
	var newly []int
	for pid, p := range procs {
		if pid <= 1 || pid == self || inJail[pid] || !t.targeted(p) {
			continue
		}
		origin, err := t.Sys.CgroupOf(pid)
		if err != nil {
			continue // exited meanwhile
		}
		if err := t.Sys.Move(pid, t.Jail); err != nil {
			t.Log.Warn("não consegui isolar processo", "pid", pid, "name", p.Comm, "err", err)
			continue
		}
		t.origins[pid] = origin
		inJail[pid] = true
		newly = append(newly, pid)
		t.Log.Info("rede cortada", "pid", pid, "name", name(p))
	}
	for _, pid := range newly {
		t.killConns(ctx, pid)
	}

	for pid := range t.origins {
		if _, alive := procs[pid]; !alive {
			delete(t.origins, pid)
		}
	}
	t.jailed = t.jailed[:0]
	for pid := range inJail {
		if p, ok := procs[pid]; ok {
			t.jailed = append(t.jailed, backend.JailedProc{PID: pid, Name: name(p)})
		}
	}
	slices.SortFunc(t.jailed, func(a, b backend.JailedProc) int { return a.PID - b.PID })
	return nil
}

func name(p Proc) string {
	if p.Exe != "" {
		return p.Exe
	}
	return p.Comm
}

// keep: a jailed process stays while it, or a jailed ancestor, is targeted.
// That keeps e.g. games launched by Steam offline along with Steam.
func (t *Tracker) keep(pid int, procs map[int]Proc, inJail map[int]bool) bool {
	for range 64 {
		p, ok := procs[pid]
		if !ok || !inJail[pid] {
			return false
		}
		if t.targeted(p) {
			return true
		}
		pid = p.PPID
	}
	return false
}

// originOf is the cgroup pid (or its closest recorded ancestor) came from.
func (t *Tracker) originOf(pid int, procs map[int]Proc) string {
	for range 64 {
		if o, ok := t.origins[pid]; ok {
			return o
		}
		p, ok := procs[pid]
		if !ok {
			break
		}
		pid = p.PPID
	}
	return ""
}

// release moves pid back to origin, or the closest ancestor cgroup that still
// accepts it (the app scope may be gone once we emptied it).
func (t *Tracker) release(pid int, origin string) {
	for cg := origin; ; cg = filepath.Dir(cg) {
		if cg == "." || cg == "/" {
			cg = ""
		}
		if err := t.Sys.Move(pid, cg); err == nil {
			t.Log.Info("rede liberada", "pid", pid, "cgroup", "/"+cg)
			break
		}
		if cg == "" {
			t.Log.Warn("não consegui liberar processo", "pid", pid)
			break
		}
	}
	delete(t.origins, pid)
}

// killConns destroys the connected sockets pid opened before being jailed.
func (t *Tracker) killConns(ctx context.Context, pid int) {
	conns, err := t.Sys.Conns(pid)
	if err != nil || len(conns) == 0 {
		return
	}
	var exprs []string
	for _, c := range conns {
		exprs = append(exprs, fmt.Sprintf("( src %s and sport = :%d and dst %s and dport = :%d )",
			ssAddr(c.Src.Addr()), c.Src.Port(), ssAddr(c.Dst.Addr()), c.Dst.Port()))
	}
	args := []string{"-K", "-n", "-t", "-u", "state", "all"}
	args = append(args, strings.Join(exprs, " or "))
	if _, err := t.Exec.Run(ctx, nil, "ss", args...); err != nil {
		t.Log.Warn("ss -K falhou", "pid", pid, "err", err)
	}
}

func ssAddr(a netip.Addr) string {
	if a.Is6() && !a.Is4In6() {
		return "[" + a.String() + "]"
	}
	return a.Unmap().String()
}

// ── Linux implementation ───────────────────────────────────────────────

type LinuxSystem struct {
	CgroupRoot string
	ProcRoot   string
}

func (l LinuxSystem) Procs() (map[int]Proc, error) {
	ents, err := os.ReadDir(l.ProcRoot)
	if err != nil {
		return nil, err
	}
	out := map[int]Proc{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(l.ProcRoot, e.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		// stat: "pid (comm) state ppid ..."; comm may contain spaces/parens.
		s := string(stat)
		lp, rp := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if lp < 0 || rp < lp {
			continue
		}
		fields := strings.Fields(s[rp+1:])
		if len(fields) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		p := Proc{PID: pid, PPID: ppid, Comm: s[lp+1 : rp]}
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			p.Exe = filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
		}
		out[pid] = p
	}
	return out, nil
}

func (l LinuxSystem) CgroupOf(pid int) (string, error) {
	b, err := os.ReadFile(filepath.Join(l.ProcRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.Trim(rest, "/"), nil
		}
	}
	return "", fmt.Errorf("pid %d sem cgroup v2", pid)
}

func (l LinuxSystem) CgroupProcs(cg string) ([]int, error) {
	b, err := os.ReadFile(filepath.Join(l.CgroupRoot, cg, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(f); err == nil {
			out = append(out, pid)
		}
	}
	return out, nil
}

func (l LinuxSystem) Move(pid int, cg string) error {
	return os.WriteFile(filepath.Join(l.CgroupRoot, cg, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0)
}

func (l LinuxSystem) MkCgroup(cg string) error {
	err := os.Mkdir(filepath.Join(l.CgroupRoot, cg), 0o755)
	if os.IsExist(err) {
		return nil
	}
	return err
}

// Conns lists connected sockets of pid by matching its fd inodes against
// /proc/pid/net/{tcp,tcp6,udp,udp6}.
func (l LinuxSystem) Conns(pid int) ([]Conn, error) {
	dir := filepath.Join(l.ProcRoot, strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return nil, err
	}
	inodes := map[string]bool{}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err == nil && strings.HasPrefix(link, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	var out []Conn
	for _, tbl := range []string{"tcp", "tcp6", "udp", "udp6"} {
		b, err := os.ReadFile(filepath.Join(dir, "net", tbl))
		if err != nil {
			continue
		}
		out = append(out, ParseConns(string(b), strings.HasPrefix(tbl, "udp"), inodes)...)
	}
	return out, nil
}

// ParseConns parses a /proc/net/{tcp,udp}[6] table, keeping connected sockets
// whose inode is in inodes.
func ParseConns(table string, udp bool, inodes map[string]bool) []Conn {
	var out []Conn
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 10 || !inodes[f[9]] {
			continue
		}
		src, err1 := parseHexAddrPort(f[1])
		dst, err2 := parseHexAddrPort(f[2])
		if err1 != nil || err2 != nil || dst.Port() == 0 {
			continue // listening or unconnected
		}
		out = append(out, Conn{UDP: udp, Src: src, Dst: dst})
	}
	return out
}

// parseHexAddrPort decodes "0100007F:0035": the address is stored as
// 32-bit words in host (little-endian) order.
func parseHexAddrPort(s string) (netip.AddrPort, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok || (len(h) != 8 && len(h) != 32) {
		return netip.AddrPort{}, fmt.Errorf("endereço inválido %q", s)
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	var raw [16]byte
	n := len(h) / 2
	for w := 0; w < n/4; w++ {
		for i := range 4 {
			v, err := strconv.ParseUint(h[w*8+(3-i)*2:w*8+(3-i)*2+2], 16, 8)
			if err != nil {
				return netip.AddrPort{}, err
			}
			raw[w*4+i] = byte(v)
		}
	}
	var a netip.Addr
	if n == 4 {
		a = netip.AddrFrom4([4]byte(raw[:4]))
	} else {
		a = netip.AddrFrom16(raw).Unmap()
	}
	return netip.AddrPortFrom(a, uint16(port)), nil
}
