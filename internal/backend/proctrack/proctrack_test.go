package proctrack

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

// fakeSys keeps a pid -> cgroup map; cgroups listed in busy refuse processes.
type fakeSys struct {
	procs map[int]Proc
	cg    map[int]string
	busy  map[string]bool
	conns map[int][]Conn
}

func (f *fakeSys) Procs() (map[int]Proc, error)     { return f.procs, nil }
func (f *fakeSys) CgroupOf(pid int) (string, error) { return f.cg[pid], nil }
func (f *fakeSys) MkCgroup(string) error            { return nil }
func (f *fakeSys) Conns(pid int) ([]Conn, error)    { return f.conns[pid], nil }
func (f *fakeSys) CgroupProcs(cg string) ([]int, error) {
	var out []int
	for pid, c := range f.cg {
		if c == cg {
			out = append(out, pid)
		}
	}
	slices.Sort(out)
	return out, nil
}
func (f *fakeSys) Move(pid int, cg string) error {
	if f.busy[cg] {
		return io.ErrClosedPipe
	}
	f.cg[pid] = cg
	return nil
}

const appScope = "user.slice/user-1000.slice/user@1000.service/app.slice/app-discord.scope"

func setup() (*Tracker, *fakeSys, *sysexec.Recorder) {
	sys := &fakeSys{
		procs: map[int]Proc{
			1:   {PID: 1, Comm: "systemd", Exe: "systemd"},
			100: {PID: 100, PPID: 1, Comm: "Discord", Exe: "Discord"},
			101: {PID: 101, PPID: 100, Comm: "Discord", Exe: "Discord"},
			200: {PID: 200, PPID: 1, Comm: "steam", Exe: "steam"},
			201: {PID: 201, PPID: 200, Comm: "hl2_linux", Exe: "hl2_linux"},
			300: {PID: 300, PPID: 1, Comm: "telegram-deskto", Exe: "telegram-desktop"},
			400: {PID: 400, PPID: 1, Comm: "firefox", Exe: "firefox"},
		},
		cg:   map[int]string{1: "init.scope", 100: appScope, 101: appScope, 200: "s", 201: "s", 300: "t", 400: "f"},
		busy: map[string]bool{},
		conns: map[int][]Conn{100: {{
			Src: netip.MustParseAddrPort("192.168.0.5:50000"),
			Dst: netip.MustParseAddrPort("[2606:4700::1]:443"),
		}}},
	}
	rec := &sysexec.Recorder{}
	t := &Tracker{Sys: sys, Exec: rec, Jail: "blockd.jail", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return t, sys, rec
}

func jailed(sys *fakeSys) []int { p, _ := sys.CgroupProcs("blockd.jail"); return p }

func TestJailAndRelease(t *testing.T) {
	tr, sys, rec := setup()
	ctx := context.Background()
	tr.SetTargets([]string{"Discord", "telegram-desktop"})
	if err := tr.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if got := jailed(sys); !slices.Equal(got, []int{100, 101, 300}) {
		t.Fatalf("jailed = %v (telegram must match by exe, not truncated comm)", got)
	}
	if len(rec.Calls) != 1 || !strings.Contains(rec.Calls[0].String(),
		"( src 192.168.0.5 and sport = :50000 and dst [2606:4700::1] and dport = :443 )") {
		t.Fatalf("ss calls = %v", rec.Calls)
	}
	if got := tr.Jailed(); len(got) != 3 || got[0].Name != "Discord" {
		t.Fatalf("Jailed() = %v", got)
	}

	tr.SetTargets([]string{"telegram-desktop"})
	_ = tr.Scan(ctx)
	if got := jailed(sys); !slices.Equal(got, []int{300}) {
		t.Fatalf("jailed = %v", got)
	}
	if sys.cg[100] != appScope || sys.cg[101] != appScope {
		t.Fatalf("discord should go back to its scope: %v", sys.cg)
	}
}

func TestChildrenStayWithTargetedParent(t *testing.T) {
	tr, sys, _ := setup()
	ctx := context.Background()
	tr.SetTargets([]string{"steam"})
	_ = tr.Scan(ctx)
	// A game forked inside the jail inherits it; it is not a target itself.
	sys.cg[201] = "blockd.jail"
	_ = tr.Scan(ctx)
	if got := jailed(sys); !slices.Equal(got, []int{200, 201}) {
		t.Fatalf("jailed = %v", got)
	}
	_ = tr.Restore(ctx)
	if got := jailed(sys); len(got) != 0 {
		t.Fatalf("restore left %v", got)
	}
	if sys.cg[201] != "s" {
		t.Fatalf("child should follow parent's origin, got %q", sys.cg[201])
	}
}

func TestReleaseFallsBackToAncestorCgroup(t *testing.T) {
	tr, sys, _ := setup()
	ctx := context.Background()
	tr.SetTargets([]string{"Discord"})
	_ = tr.Scan(ctx)
	// The app scope disappeared after we emptied it.
	sys.busy[appScope] = true
	sys.busy[filepath.Dir(appScope)] = true
	tr.SetTargets(nil)
	_ = tr.Scan(ctx)
	if want := "user.slice/user-1000.slice/user@1000.service"; sys.cg[100] != want {
		t.Fatalf("cg = %q, want %q", sys.cg[100], want)
	}
}

func TestParseConns(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 111 1 0000000000000000 100 0 0 10 0
   1: 0500A8C0:C350 0101A8C0:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 20 4 30 10 -1
   2: 0500A8C0:C351 0101A8C0:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 20 4 30 10 -1`
	got := ParseConns(tcp, false, map[string]bool{"111": true, "222": true})
	if len(got) != 1 || got[0].Src.String() != "192.168.0.5:50000" || got[0].Dst.String() != "192.168.1.1:443" {
		t.Fatalf("got %+v", got)
	}
	tcp6 := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000001000000:A000 0047062600000000000000000100000:01BB 01 0 0 0 0 0 0 444`
	_ = tcp6 // malformed on purpose (31 hex chars): must be skipped without panic
	if got := ParseConns(tcp6, false, map[string]bool{"444": true}); len(got) != 0 {
		t.Fatalf("malformed row parsed: %+v", got)
	}
	v6 := `header
   0: 00000000000000000000000001000000:A000 00470626000000000000000001000000:01BB 01 00000000:00000000 00:00000000 00000000  1000 0 555 1`
	got = ParseConns(v6, false, map[string]bool{"555": true})
	if len(got) != 1 || got[0].Src.String() != "[::1]:40960" || got[0].Dst.String() != "[2606:4700::1]:443" {
		t.Fatalf("v6 got %+v", got)
	}
}
