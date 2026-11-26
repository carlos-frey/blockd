//go:build windows

package wfp

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/carlos-frey/blockd/internal/backend"
)

const (
	groupNet   = "net"
	groupDyn   = "dyn"
	jailPrefix = "jail:"
)

// Blocker implements backend.NetworkBlocker. NetPlan.JailProcesses needs no
// filter of its own here: the jail filters are per program and owned by
// Jail, driven by the process tracker.
type Blocker struct{ S *Session }

var _ backend.NetworkBlocker = (*Blocker)(nil)

func NewBlocker(s *Session) *Blocker { return &Blocker{S: s} }

func (b *Blocker) Apply(_ context.Context, plan backend.NetPlan) error {
	if plan.Empty() {
		return errors.Join(b.S.remove(groupNet), b.S.remove(groupDyn))
	}
	if err := b.S.replace(groupNet, backend.Fingerprint(plan), "", NetSpecs(plan)); err != nil {
		return err
	}
	// Dynamic sets start empty after Apply, as with nft.
	return b.S.remove(groupDyn)
}

func (b *Blocker) SetDynamic(_ context.Context, addrs []netip.Addr) error {
	specs := DynSpecs(addrs)
	if len(specs) == 0 {
		return b.S.remove(groupDyn)
	}
	return b.S.replace(groupDyn, "", "", specs)
}

func (b *Blocker) Fingerprint(context.Context) (string, error) { return b.S.check(groupNet) }

// Teardown removes every blockd object from WFP, jail filters included.
func (b *Blocker) Teardown(context.Context) error { return b.S.purge() }

// Jail cuts programs off the network by path; winproc.Tracker drives it.
type Jail struct{ S *Session }

func NewJail(s *Session) *Jail { return &Jail{S: s} }

func jailKey(path string) string { return jailPrefix + strings.ToLower(path) }

// Paths lists the jailed programs whose filters are all in place. A program
// whose filters were tampered with has the rest removed and drops out of the
// list; the tracker adds it back if it is still a target.
func (j *Jail) Paths() ([]string, error) {
	intact, broken, err := j.S.groupsWithPrefix(jailPrefix)
	if err != nil {
		return nil, err
	}
	var errs []error
	for name := range broken {
		errs = append(errs, j.S.remove(name))
	}
	out := make([]string, 0, len(intact))
	for _, g := range intact {
		out = append(out, g.Path)
	}
	return out, errors.Join(errs...)
}

func (j *Jail) Add(path string) error { return j.S.replace(jailKey(path), "", path, JailSpecs(path)) }

func (j *Jail) Remove(path string) error { return j.S.remove(jailKey(path)) }
