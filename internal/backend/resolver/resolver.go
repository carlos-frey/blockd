// Package resolver resolves names against the real upstream servers, skipping
// the local sinkhole, so blocked domains can still be turned into IPs.
package resolver

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/carlos-frey/blockd/internal/backend"
)

type Upstream struct {
	ResolvFile string   // e.g. /run/NetworkManager/no-stub-resolv.conf
	Fallback   []string // used when the file has no usable server
	next       atomic.Uint32
}

var _ backend.Resolver = (*Upstream)(nil)

func (u *Upstream) servers() []string {
	var out []string
	if f, err := os.Open(u.ResolvFile); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			a, err := netip.ParseAddr(fields[1])
			if err != nil || a.IsLoopback() {
				continue // never ask ourselves or the resolved stub
			}
			out = append(out, fields[1])
		}
	}
	if len(out) == 0 {
		out = slices.Clone(u.Fallback)
	}
	return out
}

func (u *Upstream) LookupIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	servers := u.servers()
	if len(servers) == 0 {
		return nil, errors.New("nenhum servidor DNS upstream")
	}
	server := servers[int(u.next.Add(1))%len(servers)]
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
		},
	}
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, nil
}
