// Package fake provides in-memory backends for tests and `blockd --dry-run`.
package fake

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"sync"

	"github.com/carlos-frey/blockd/internal/backend"
)

// Network records the applied plan.
type Network struct {
	mu      sync.Mutex
	Plan    backend.NetPlan
	Dynamic []netip.Addr
	Applies int
	Err     error
	Log     *slog.Logger
}

func (n *Network) Apply(_ context.Context, p backend.NetPlan) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Err != nil {
		return n.Err
	}
	n.Plan, n.Dynamic = p, nil
	n.Applies++
	logf(n.Log, "fake nft: apply", "jail", p.JailProcesses, "dynamic", p.Dynamic, "static", len(p.Static))
	return nil
}

func (n *Network) SetDynamic(_ context.Context, a []netip.Addr) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Dynamic = slices.Clone(a)
	logf(n.Log, "fake nft: dynamic set", "addrs", len(a))
	return nil
}

func (n *Network) Fingerprint(context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return backend.Fingerprint(n.Plan), nil
}

func (n *Network) Teardown(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Plan, n.Dynamic = backend.NetPlan{}, nil
	return nil
}

// Tamper simulates someone flushing the rules behind our back.
func (n *Network) Tamper() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Plan = backend.NetPlan{}
}

// DNS records the sinkholed domains.
type DNS struct {
	mu      sync.Mutex
	Plan    backend.DNSPlan
	Applies int
	Log     *slog.Logger
}

func (d *DNS) Apply(_ context.Context, p backend.DNSPlan) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Plan = p
	d.Applies++
	logf(d.Log, "fake dns: apply", "domains", len(p.Domains))
	return nil
}

func (d *DNS) Fingerprint(context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return backend.Fingerprint(d.Plan), nil
}

func (d *DNS) Teardown(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Plan = backend.DNSPlan{}
	return nil
}

// Procs keeps the target list; nothing is ever jailed.
type Procs struct {
	mu      sync.Mutex
	Targets []string
}

func (p *Procs) Prepare() error { return nil }
func (p *Procs) SetTargets(n []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Targets = slices.Clone(n)
}
func (p *Procs) Run(ctx context.Context) error { <-ctx.Done(); return nil }
func (p *Procs) Jailed() []backend.JailedProc  { return nil }
func (p *Procs) Restore(context.Context) error { p.SetTargets(nil); return nil }

// Kicker counts kicks.
type Kicker struct {
	mu    sync.Mutex
	Kicks int
	Log   *slog.Logger
}

func (k *Kicker) Kick(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.Kicks++
	logf(k.Log, "fake kick")
	return nil
}

func (k *Kicker) Count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.Kicks
}

// Resolver answers from a fixed table.
type Resolver map[string][]netip.Addr

func (r Resolver) LookupIPs(_ context.Context, host string) ([]netip.Addr, error) {
	return r[host], nil
}

func logf(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Info(msg, args...)
	}
}
