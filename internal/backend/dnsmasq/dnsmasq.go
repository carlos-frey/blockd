// Package dnsmasq sinkholes domains with a dedicated dnsmasq instance that
// systemd-resolved forwards every query to while something is blocked.
//
//	app → 127.0.0.53 (resolved) → 127.0.0.153 (blockd-dns/dnsmasq) → upstream real
//
// Blocked names answer NXDOMAIN (local=/domain/), which also covers every
// subdomain. When nothing is blocked the resolved drop-in is removed and the
// dnsmasq unit stopped, so the system DNS is left exactly as it was.
package dnsmasq

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

type Config struct {
	ConfPath   string // generated dnsmasq config
	ListenAddr string // where dnsmasq listens
	ListenPort int
	ResolvFile string   // upstream servers kept up to date by NetworkManager
	Fallback   []string // upstream servers when ResolvFile does not exist
	DropIn     string   // systemd-resolved drop-in
	Unit       string   // systemd unit running dnsmasq
}

func DefaultConfig() Config {
	return Config{
		ConfPath:   "/var/lib/blockd/dnsmasq.conf",
		ListenAddr: "127.0.0.153",
		ListenPort: 53,
		ResolvFile: "/run/NetworkManager/no-stub-resolv.conf",
		Fallback:   []string{"9.9.9.9", "1.1.1.1"},
		DropIn:     "/etc/systemd/resolved.conf.d/blockd.conf",
		Unit:       "blockd-dns.service",
	}
}

type Blocker struct {
	Cfg Config
	Run sysexec.Runner
}

func New(cfg Config) *Blocker { return &Blocker{Cfg: cfg, Run: sysexec.Exec{}} }

var _ backend.DNSBlocker = (*Blocker)(nil)

// Conf renders the dnsmasq configuration for a plan.
func (b *Blocker) Conf(plan backend.DNSPlan) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# blockd fp=%s — gerado automaticamente, não edite.\n", backend.Fingerprint(plan))
	fmt.Fprintf(&sb, "listen-address=%s\nbind-interfaces\nport=%d\n", b.Cfg.ListenAddr, b.Cfg.ListenPort)
	sb.WriteString("pid-file=\nno-hosts\ncache-size=2000\n")
	if _, err := os.Stat(b.Cfg.ResolvFile); err == nil {
		fmt.Fprintf(&sb, "resolv-file=%s\n", b.Cfg.ResolvFile)
	} else {
		sb.WriteString("no-resolv\n")
		for _, s := range b.Cfg.Fallback {
			fmt.Fprintf(&sb, "server=%s\n", s)
		}
	}
	for _, d := range plan.Domains {
		fmt.Fprintf(&sb, "local=/%s/\n", d)
	}
	return sb.String()
}

// DropIn renders the resolved configuration that routes all lookups to us.
// The global "~." routing domain wins over links that are only default routes,
// so queries are not raced against the ISP servers.
func (b *Blocker) DropIn() string {
	addr := b.Cfg.ListenAddr
	if b.Cfg.ListenPort != 53 {
		addr = fmt.Sprintf("%s:%d", addr, b.Cfg.ListenPort)
	}
	return fmt.Sprintf("# Gerenciado pelo blockd — removido quando nada estiver bloqueado.\n[Resolve]\nDNS=%s\nDomains=~.\n", addr)
}

func (b *Blocker) Apply(ctx context.Context, plan backend.DNSPlan) error {
	if plan.Empty() {
		return b.Teardown(ctx)
	}
	conf := b.Conf(plan)
	if err := writeAtomic(b.Cfg.ConfPath, conf); err != nil {
		return err
	}
	if _, err := b.Run.Run(ctx, nil, "dnsmasq", "--test", "--conf-file="+b.Cfg.ConfPath); err != nil {
		return fmt.Errorf("config do dnsmasq inválida: %w", err)
	}
	if _, err := b.Run.Run(ctx, nil, "systemctl", "restart", b.Cfg.Unit); err != nil {
		return err
	}
	changed, err := writeIfChanged(b.Cfg.DropIn, b.DropIn())
	if err != nil {
		return err
	}
	if changed {
		if _, err := b.Run.Run(ctx, nil, "systemctl", "reload-or-restart", "systemd-resolved.service"); err != nil {
			return err
		}
	}
	return b.flush(ctx)
}

func (b *Blocker) Teardown(ctx context.Context) error {
	var errs []error
	err := os.Remove(b.Cfg.DropIn)
	switch {
	case err == nil:
		if _, err := b.Run.Run(ctx, nil, "systemctl", "reload-or-restart", "systemd-resolved.service"); err != nil {
			errs = append(errs, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		errs = append(errs, err)
	}
	if err := os.Remove(b.Cfg.ConfPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	if _, err := b.Run.Run(ctx, nil, "systemctl", "stop", b.Cfg.Unit); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, b.flush(ctx))
	return errors.Join(errs...)
}

// flush drops names cached by resolved before the change.
func (b *Blocker) flush(ctx context.Context) error {
	_, err := b.Run.Run(ctx, nil, "resolvectl", "flush-caches")
	return err
}

var fpRe = regexp.MustCompile(`^# blockd fp=([0-9a-f]*) `)

// Fingerprint is the one in the generated config, but only while the whole
// chain is in place: resolved drop-in intact and dnsmasq running.
func (b *Blocker) Fingerprint(ctx context.Context) (string, error) {
	conf, err := os.ReadFile(b.Cfg.ConfPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	m := fpRe.FindSubmatch(conf)
	if m == nil {
		return "", nil
	}
	dropIn, err := os.ReadFile(b.Cfg.DropIn)
	if err != nil || string(dropIn) != b.DropIn() {
		return "", nil
	}
	out, _ := b.Run.Run(ctx, nil, "systemctl", "is-active", b.Cfg.Unit)
	if strings.TrimSpace(string(out)) != "active" {
		return "", nil
	}
	return string(m[1]), nil
}

func writeIfChanged(path, content string) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, []byte(content)) {
		return false, nil
	}
	return true, writeAtomic(path, content)
}

func writeAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
