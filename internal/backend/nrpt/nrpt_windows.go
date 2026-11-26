//go:build windows

package nrpt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/winsys"
)

const (
	// Local NRPT rules, one subkey per rule.
	localBase = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	// Group Policy rules; when there is any, Windows ignores the local ones.
	gpBase = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`

	// Our subkeys share this GUID prefix; the rule index fills the rest.
	keyPrefix = "{6b6c6f63-6b64-4e52-9054-"

	ruleVersion       = 2
	configOverrideDNS = 0x8 // ConfigOptions: use GenericDNSServers
)

func keyName(i int) string { return fmt.Sprintf("%s%012x}", keyPrefix, i) }

func ours(name string) bool { return strings.HasPrefix(strings.ToLower(name), keyPrefix) }

type Config struct {
	// SinkholeAddr is where the sinkhole listens; NRPT always uses port 53.
	SinkholeAddr string
}

func DefaultConfig() Config { return Config{SinkholeAddr: "127.0.0.153"} }

// Blocker implements backend.DNSBlocker with NRPT rules and the sinkhole.
type Blocker struct {
	Cfg  Config
	Log  *slog.Logger
	sink *Sinkhole
}

var _ backend.DNSBlocker = (*Blocker)(nil)

func New(cfg Config, log *slog.Logger) *Blocker {
	return &Blocker{Cfg: cfg, Log: log, sink: &Sinkhole{Addr: net.JoinHostPort(cfg.SinkholeAddr, "53"), Log: log}}
}

func (b *Blocker) Apply(_ context.Context, plan backend.DNSPlan) error {
	if plan.Empty() {
		return b.Teardown(context.Background())
	}
	if gpRulesExist() {
		return errors.New("há regras NRPT de Política de Grupo neste Windows; com elas, as regras locais do blockd são ignoradas")
	}
	if err := b.sink.Start(); err != nil {
		return fmt.Errorf("sinkhole DNS em %s: %w", b.sink.Addr, err)
	}
	rules := Namespaces(plan.Domains)
	comment := Comment(backend.Fingerprint(plan), len(rules))

	base, _, err := registry.CreateKey(registry.LOCAL_MACHINE, localBase, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("registro NRPT: %w", err)
	}
	defer base.Close()
	want := map[string]bool{}
	for i, names := range rules {
		name := keyName(i)
		want[strings.ToLower(name)] = true
		if err := b.writeRule(base, name, names, comment); err != nil {
			return fmt.Errorf("regra NRPT %s: %w", name, err)
		}
	}
	existing, err := base.ReadSubKeyNames(-1)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range existing {
		if ours(name) && !want[strings.ToLower(name)] {
			if err := registry.DeleteKey(base, name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	errs = append(errs, winsys.FlushDNSCache())
	return errors.Join(errs...)
}

func (b *Blocker) writeRule(base registry.Key, name string, names []string, comment string) error {
	k, _, err := registry.CreateKey(base, name, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	return errors.Join(
		k.SetDWordValue("Version", ruleVersion),
		k.SetStringsValue("Name", names),
		k.SetStringValue("GenericDNSServers", b.Cfg.SinkholeAddr),
		k.SetDWordValue("ConfigOptions", configOverrideDNS),
		k.SetStringValue("IPSECCARestriction", ""),
		k.SetStringValue("DisplayName", "blockd"),
		k.SetStringValue("Comment", comment),
	)
}

// Fingerprint is the one in the rules, while every rule is in place, points
// to the sinkhole, and the sinkhole runs in this process.
func (b *Blocker) Fingerprint(context.Context) (string, error) {
	base, err := registry.OpenKey(registry.LOCAL_MACHINE, localBase, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer base.Close()
	names, err := base.ReadSubKeyNames(-1)
	if err != nil {
		return "", err
	}
	var comments []string
	for _, name := range names {
		if !ours(name) {
			continue
		}
		k, err := registry.OpenKey(base, name, registry.READ)
		if err != nil {
			return "", nil
		}
		comment, _, err1 := k.GetStringValue("Comment")
		server, _, err2 := k.GetStringValue("GenericDNSServers")
		ns, _, err3 := k.GetStringsValue("Name")
		k.Close()
		if err1 != nil || err2 != nil || err3 != nil || server != b.Cfg.SinkholeAddr || len(ns) == 0 {
			return "", nil
		}
		comments = append(comments, comment)
	}
	fp := FingerprintOf(comments)
	if fp != "" && (!b.sink.Running() || gpRulesExist()) {
		return "", nil
	}
	return fp, nil
}

func (b *Blocker) Teardown(context.Context) error {
	var errs []error
	base, err := registry.OpenKey(registry.LOCAL_MACHINE, localBase, registry.ALL_ACCESS)
	switch {
	case errors.Is(err, registry.ErrNotExist):
	case err != nil:
		errs = append(errs, err)
	default:
		names, err := base.ReadSubKeyNames(-1)
		errs = append(errs, err)
		for _, name := range names {
			if ours(name) {
				errs = append(errs, registry.DeleteKey(base, name))
			}
		}
		base.Close()
	}
	b.sink.Stop()
	errs = append(errs, winsys.FlushDNSCache())
	return errors.Join(errs...)
}

func gpRulesExist() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, gpBase, registry.READ)
	if err != nil {
		return false
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	return len(names) > 0
}
