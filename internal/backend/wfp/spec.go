// Package wfp enforces the network plan on Windows with the Windows Filtering
// Platform, the kernel filter under Windows Firewall. It is the counterpart
// of the nft backend:
//
//   - every filter belongs to a blockd provider and sublayer, so nothing else
//     is touched, and works whether Windows Firewall is on or off (antivirus
//     suites often turn it off);
//   - filters are persistent: stopping the service does not lift the block,
//     and they are in place at boot before the service starts;
//   - connections are blocked at ALE_AUTH_CONNECT, so connect() fails at once
//     instead of timing out (the "reject" of the Linux ruleset).
//
// The jail is a filter per program path (ALE_APP_ID) instead of a cgroup;
// winproc decides which paths go in it.
//
// This file is the pure part: what filters a plan turns into. It builds on
// every platform so it can be unit tested anywhere.
package wfp

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/carlos-frey/blockd/internal/backend"
)

// IP protocol numbers.
const (
	ProtoTCP uint8 = 6
	ProtoUDP uint8 = 17
)

// Spec describes one block filter. Values inside a field are alternatives
// (WFP ORs conditions on the same field and ANDs different fields).
type Spec struct {
	V6       bool
	Prefixes []netip.Prefix // remote addresses; empty = any
	Protos   []uint8        // empty = any
	Ports    []uint16       // remote ports; empty = any
	App      string         // program path; "" = every program
	// SkipLoopback leaves loopback traffic alone (apps talk to their own
	// helpers over 127.0.0.1).
	SkipLoopback bool
}

// BlocksEverything reports a filter with no condition at all, which would
// cut the whole machine off. The Windows side refuses to install it.
func (s Spec) BlocksEverything() bool {
	return len(s.Prefixes) == 0 && len(s.Ports) == 0 && len(s.Protos) == 0 && s.App == ""
}

// NetSpecs turns the static part of a plan into filters: one per address
// family and port list, as the nft ruleset groups them. Dynamic addresses go
// through DynSpecs; the process jail through JailSpecs.
func NetSpecs(plan backend.NetPlan) []Spec {
	type key struct {
		v6    bool
		ports string
	}
	groups := map[key]*Spec{}
	var keys []key
	for _, r := range plan.Static {
		p, ok := normPrefix(r.Prefix)
		if !ok {
			continue
		}
		k := key{v6: p.Addr().Is6(), ports: portKey(r.Ports)}
		sp, found := groups[k]
		if !found {
			sp = &Spec{V6: k.v6}
			if len(r.Ports) > 0 {
				sp.Protos = []uint8{ProtoTCP, ProtoUDP}
				sp.Ports = sortedPorts(r.Ports)
			}
			groups[k] = sp
			keys = append(keys, k)
		}
		if !slices.Contains(sp.Prefixes, p) {
			sp.Prefixes = append(sp.Prefixes, p)
		}
	}
	slices.SortFunc(keys, func(a, b key) int {
		if a.v6 != b.v6 {
			if a.v6 {
				return 1
			}
			return -1
		}
		return strings.Compare(a.ports, b.ports)
	})
	out := make([]Spec, 0, len(keys))
	for _, k := range keys {
		out = append(out, *groups[k])
	}
	return out
}

// DynSpecs blocks the resolved addresses of ip_block domains, one filter per
// family (none for a family without addresses).
func DynSpecs(addrs []netip.Addr) []Spec {
	var v4, v6 Spec
	v6.V6 = true
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() {
			continue
		}
		p := netip.PrefixFrom(a, a.BitLen())
		if a.Is4() {
			v4.Prefixes = append(v4.Prefixes, p)
		} else {
			v6.Prefixes = append(v6.Prefixes, p)
		}
	}
	var out []Spec
	for _, s := range []Spec{v4, v6} {
		if len(s.Prefixes) > 0 {
			slices.SortFunc(s.Prefixes, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
			s.Prefixes = slices.Compact(s.Prefixes)
			out = append(out, s)
		}
	}
	return out
}

// JailSpecs cuts every outbound connection of the program at path, except
// to loopback.
func JailSpecs(path string) []Spec {
	return []Spec{
		{App: path, SkipLoopback: true},
		{V6: true, App: path, SkipLoopback: true},
	}
}

// normPrefix maps IPv4-mapped IPv6 prefixes to plain IPv4 and masks the host
// bits, so each family gets the right layer.
func normPrefix(p netip.Prefix) (netip.Prefix, bool) {
	if !p.IsValid() {
		return netip.Prefix{}, false
	}
	if a := p.Addr(); a.Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, false
		}
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), true
}

func sortedPorts(ports []uint16) []uint16 {
	out := slices.Clone(ports)
	slices.Sort(out)
	return slices.Compact(out)
}

func portKey(ports []uint16) string {
	var sb strings.Builder
	for i, p := range sortedPorts(ports) {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(int(p)))
	}
	return sb.String()
}
