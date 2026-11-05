package nft

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
	"github.com/carlos-frey/blockd/internal/domain"
)

var update = flag.Bool("update", false, "rewrite golden files")

func samplePlan() backend.NetPlan {
	mk := func(s string, ports ...uint16) domain.IPRule {
		p, _ := domain.ParsePrefix(s)
		return domain.IPRule{Prefix: p, Ports: ports}
	}
	return backend.NetPlan{
		JailProcesses: true,
		Dynamic:       true,
		Static: []domain.IPRule{
			mk("1.1.1.1", 443, 853), mk("8.8.8.8", 443, 853),
			mk("2606:4700:4700::1111", 443, 853), mk("10.9.0.0/16"),
		},
	}
}

func TestRulesetGolden(t *testing.T) {
	got := Ruleset(samplePlan(), "blockd.jail")
	path := "testdata/ruleset.nft"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("ruleset differs from golden (run with -update):\n%s", got)
	}
}

func TestEmptyPlanOnlyDeletes(t *testing.T) {
	got := Ruleset(backend.NetPlan{}, "blockd.jail")
	if got != "table inet blockd\ndelete table inet blockd\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFingerprintDetectsTampering(t *testing.T) {
	plan := backend.NetPlan{Dynamic: true}
	fp := backend.Fingerprint(plan)
	listing := func(rules int) string {
		var sb strings.Builder
		sb.WriteString(`{"nftables":[{"metainfo":{}},{"table":{"family":"inet","name":"blockd","comment":"blockd fp=` + fp + ` rules=2"}}`)
		for range rules {
			sb.WriteString(`,{"rule":{"family":"inet","table":"blockd","chain":"output"}}`)
		}
		sb.WriteString(`,{"rule":{"family":"inet","table":"blockd","chain":"deny"}}]}`)
		return sb.String()
	}
	for _, tc := range []struct {
		rules int
		want  string
	}{{2, fp}, {1, ""}} {
		b := &Blocker{Bin: "nft", Run: &sysexec.Recorder{Respond: func(sysexec.Call) ([]byte, error) {
			return []byte(listing(tc.rules)), nil
		}}}
		got, err := b.Fingerprint(context.Background())
		if err != nil || got != tc.want {
			t.Errorf("rules=%d: got %q, %v; want %q", tc.rules, got, err, tc.want)
		}
	}
	missing := &Blocker{Bin: "nft", Run: &sysexec.Recorder{Respond: func(sysexec.Call) ([]byte, error) {
		return nil, errors.New("Error: No such file or directory; did you mean table 'filter'?")
	}}}
	if got, err := missing.Fingerprint(context.Background()); got != "" || err != nil {
		t.Fatalf("missing table: %q, %v", got, err)
	}
}

// TestRealNft loads the ruleset into the kernel. It needs CAP_NET_ADMIN, so
// run it inside a throwaway user+network namespace:
//
//	unshare -rn go test ./internal/backend/nft -run RealNft -v
func TestRealNft(t *testing.T) {
	if err := exec.Command("nft", "list", "tables").Run(); err != nil {
		t.Skip("sem CAP_NET_ADMIN; rode com `unshare -rn`")
	}
	ctx := context.Background()
	// Any existing level-1 cgroup works for the socket match.
	b := New("user.slice")
	plan := samplePlan()
	if err := b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if fp, err := b.Fingerprint(ctx); err != nil || fp != backend.Fingerprint(plan) {
		t.Fatalf("fingerprint = %q, %v", fp, err)
	}
	addrs := []netip.Addr{netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("2001:db8::5")}
	if err := b.SetDynamic(ctx, addrs); err != nil {
		t.Fatal(err)
	}
	if err := b.SetDynamic(ctx, addrs[:1]); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("nft", "list", "set", "inet", "blockd", "dyn6").CombinedOutput()
	if strings.Contains(string(out), "2001:db8::5") {
		t.Fatalf("dyn6 should have been flushed:\n%s", out)
	}
	// Reapplying is idempotent while the table exists.
	if err := b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("nft", "delete", "rule", "inet", "blockd", "output", "handle", firstOutputHandle(t)).Run(); err != nil {
		t.Fatal(err)
	}
	if fp, _ := b.Fingerprint(ctx); fp != "" {
		t.Fatalf("tampered table should report drift, got %q", fp)
	}
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	if fp, err := b.Fingerprint(ctx); fp != "" || err != nil {
		t.Fatalf("after teardown: %q, %v", fp, err)
	}
	if err := b.Teardown(ctx); err != nil {
		t.Fatalf("teardown must be idempotent: %v", err)
	}
}

func firstOutputHandle(t *testing.T) string {
	out, err := exec.Command("nft", "-a", "list", "chain", "inet", "blockd", "output").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "jump deny # handle "); i >= 0 {
			return strings.TrimSpace(line[i+len("jump deny # handle "):])
		}
	}
	t.Fatalf("no rule handle in:\n%s", out)
	return ""
}

// TestRealNftRejects checks the deny chain on loopback inside the namespace:
//
//	unshare -rn go test ./internal/backend/nft -run RealNft -v
func TestRealNftRejects(t *testing.T) {
	if err := exec.Command("nft", "list", "tables").Run(); err != nil {
		t.Skip("sem CAP_NET_ADMIN; rode com `unshare -rn`")
	}
	if err := exec.Command("ip", "link", "set", "lo", "up").Run(); err != nil {
		t.Skip("sem ip link: ", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	dial := func() error {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if err == nil {
			c.Close()
		}
		return err
	}
	ctx := context.Background()
	b := New("blockd.jail")
	plan := backend.NetPlan{Static: []domain.IPRule{{Prefix: netip.MustParsePrefix("127.0.0.1/32"), Ports: []uint16{port}}}}
	if err := b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := dial(); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("expected connection refused (tcp reset), got %v", err)
	}
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dial(); err != nil {
		t.Fatalf("after teardown dial should work: %v", err)
	}
}
