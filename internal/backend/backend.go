// Package backend declares the enforcement interfaces the engine depends on.
// Concrete Linux implementations live in the subpackages (nft, dnsmasq,
// proctrack, resolver); fake holds in-memory versions for tests and dry runs.
package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"

	"github.com/carlos-frey/blockd/internal/domain"
)

// NetPlan is what the packet filter must enforce.
type NetPlan struct {
	JailProcesses bool            `json:"jail"`    // drop traffic from the process jail cgroup
	Dynamic       bool            `json:"dynamic"` // keep sets for resolved IPs of ip_block domains
	Static        []domain.IPRule `json:"static"`
}

func (p NetPlan) Empty() bool { return !p.JailProcesses && !p.Dynamic && len(p.Static) == 0 }

// Fingerprint identifies a desired configuration. Backends store it next to
// what they apply and report it back, so the engine can detect drift.
// An empty plan has the empty fingerprint, matching "nothing installed".
func Fingerprint(v interface{ Empty() bool }) string {
	if v.Empty() {
		return ""
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// DNSPlan is the list of sinkholed domains.
type DNSPlan struct {
	Domains []string `json:"domains"`
}

func (p DNSPlan) Empty() bool { return len(p.Domains) == 0 }

// NetworkBlocker owns the packet filter rules.
type NetworkBlocker interface {
	// Apply atomically replaces the rules; an empty plan removes them.
	// Dynamic sets start empty after Apply.
	Apply(ctx context.Context, plan NetPlan) error
	// SetDynamic atomically replaces the contents of the dynamic IP sets.
	SetDynamic(ctx context.Context, addrs []netip.Addr) error
	// Fingerprint returns the fingerprint of what is actually installed, or
	// "" when nothing is (or what is there was tampered with).
	Fingerprint(ctx context.Context) (string, error)
	Teardown(ctx context.Context) error
}

// DNSBlocker owns the DNS sinkhole.
type DNSBlocker interface {
	Apply(ctx context.Context, plan DNSPlan) error
	Fingerprint(ctx context.Context) (string, error)
	Teardown(ctx context.Context) error
}

// JailedProc is a process whose network is currently cut.
type JailedProc struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// ProcessTracker moves processes with target names into the jail cgroup and
// releases them when they stop being targets.
type ProcessTracker interface {
	// Prepare makes sure the jail exists (the firewall rule references it).
	Prepare() error
	SetTargets(names []string)
	// Run scans until ctx is done.
	Run(ctx context.Context) error
	Jailed() []JailedProc
	// Restore releases every jailed process.
	Restore(ctx context.Context) error
}

// SessionKicker makes running apps drop the DNS answers and connections they
// already had: browsers cache both, so a domain open in a tab before the
// sinkhole went up would keep working.
type SessionKicker interface {
	Kick(ctx context.Context) error
}

// Resolver looks up addresses using the real upstream, bypassing the sinkhole.
type Resolver interface {
	LookupIPs(ctx context.Context, host string) ([]netip.Addr, error)
}

// Store persists the user state.
type Store interface {
	Load() (domain.State, error)
	Save(domain.State) error
}
