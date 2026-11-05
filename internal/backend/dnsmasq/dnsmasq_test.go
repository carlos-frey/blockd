package dnsmasq

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

func testBlocker(t *testing.T) (*Blocker, *sysexec.Recorder) {
	dir := t.TempDir()
	rec := &sysexec.Recorder{Respond: func(c sysexec.Call) ([]byte, error) {
		if c.String() == "systemctl is-active blockd-dns.service" {
			return []byte("active\n"), nil
		}
		return nil, nil
	}}
	cfg := DefaultConfig()
	cfg.ConfPath = filepath.Join(dir, "dnsmasq.conf")
	cfg.DropIn = filepath.Join(dir, "resolved.conf.d", "blockd.conf")
	cfg.ResolvFile = filepath.Join(dir, "missing-resolv.conf")
	return &Blocker{Cfg: cfg, Run: rec}, rec
}

func calls(r *sysexec.Recorder) []string {
	var out []string
	for _, c := range r.Calls {
		if c.Name != "dnsmasq" {
			out = append(out, c.String())
		}
	}
	r.Calls = nil
	return out
}

func TestApplyWritesConfAndDropIn(t *testing.T) {
	b, rec := testBlocker(t)
	ctx := context.Background()
	plan := backend.DNSPlan{Domains: []string{"instagram.com", "tiktok.com"}}
	if err := b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(b.Cfg.ConfPath)
	for _, want := range []string{"local=/instagram.com/", "local=/tiktok.com/", "no-resolv", "server=9.9.9.9", "listen-address=127.0.0.153"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("conf missing %q:\n%s", want, conf)
		}
	}
	got := strings.Join(calls(rec), "; ")
	want := "systemctl restart blockd-dns.service; systemctl reload-or-restart systemd-resolved.service; resolvectl flush-caches"
	if got != want {
		t.Fatalf("calls = %s", got)
	}
	if fp, _ := b.Fingerprint(ctx); fp != backend.Fingerprint(plan) {
		t.Fatalf("fingerprint = %q", fp)
	}

	// Second apply: resolved already configured, no reload.
	_ = b.Apply(ctx, backend.DNSPlan{Domains: []string{"x.com"}})
	if got := strings.Join(calls(rec), "; "); strings.Contains(got, "systemd-resolved") {
		t.Fatalf("unexpected resolved reload: %s", got)
	}

	// Someone removes the drop-in: drift.
	os.Remove(b.Cfg.DropIn)
	if fp, _ := b.Fingerprint(ctx); fp != "" {
		t.Fatalf("missing drop-in should report drift, got %q", fp)
	}
}

func TestEmptyPlanTearsDown(t *testing.T) {
	b, rec := testBlocker(t)
	ctx := context.Background()
	_ = b.Apply(ctx, backend.DNSPlan{Domains: []string{"a.com"}})
	calls(rec)
	if err := b.Apply(ctx, backend.DNSPlan{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Cfg.DropIn); !os.IsNotExist(err) {
		t.Fatal("drop-in should be gone")
	}
	got := strings.Join(calls(rec), "; ")
	if got != "systemctl reload-or-restart systemd-resolved.service; systemctl stop blockd-dns.service; resolvectl flush-caches" {
		t.Fatalf("calls = %s", got)
	}
	if fp, _ := b.Fingerprint(ctx); fp != "" {
		t.Fatalf("fp = %q", fp)
	}
}

// TestRealDnsmasq runs the generated config with a real dnsmasq on a high
// port and checks that blocked names (and subdomains) get NXDOMAIN.
func TestRealDnsmasq(t *testing.T) {
	bin, err := exec.LookPath("dnsmasq")
	if err != nil {
		t.Skip("dnsmasq não instalado")
	}
	if _, err := exec.LookPath("dig"); err != nil {
		t.Skip("dig não instalado")
	}
	b, _ := testBlocker(t)
	b.Cfg.ListenAddr, b.Cfg.ListenPort = "127.0.0.1", 5391
	if err := os.WriteFile(b.Cfg.ConfPath, []byte(b.Conf(backend.DNSPlan{Domains: []string{"blocked.example"}})), 0o644); err != nil {
		t.Fatal(err)
	}
	u, _ := user.Current()
	g, _ := user.LookupGroupId(u.Gid)
	cmd := exec.Command(bin, "-k", "--conf-file="+b.Cfg.ConfPath, "--user="+u.Username, "--group="+g.Name)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(300 * time.Millisecond)
	for _, host := range []string{"blocked.example", "www.blocked.example"} {
		out, _ := exec.Command("dig", "@127.0.0.1", "-p", "5391", "+time=2", "+tries=1", host).CombinedOutput()
		if !strings.Contains(string(out), "status: NXDOMAIN") {
			t.Errorf("%s: expected NXDOMAIN:\n%s", host, out)
		}
	}
}
