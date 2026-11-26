//go:build windows

// Package winkick makes running browsers drop what they knew about domains
// that just got blocked, on Windows (see package kick for the Linux story).
//
// Windows offers fewer levers than Linux: there is no "network change" a
// service can fake cheaply, and only IPv4 TCP connections can be reset from
// outside the process. So the kick is:
//
//   - flush the DNS Client cache;
//   - reset the browsers' IPv4 TCP connections to port 443, so open tabs
//     have to reconnect.
//
// A browser may still reuse its own host cache (Chromium keeps system
// resolver answers for about a minute) or a QUIC session; those die out by
// themselves shortly after.
package winkick

import (
	"context"
	"errors"
	"strings"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/winsys"
)

type Kicker struct {
	// Browsers are image names, lowercase without ".exe".
	Browsers map[string]bool
	Port     uint16
}

var _ backend.SessionKicker = (*Kicker)(nil)

func New() *Kicker {
	b := map[string]bool{}
	for _, n := range []string{
		"chrome", "msedge", "firefox", "brave", "opera", "vivaldi", "chromium",
		"librewolf", "waterfox", "floorp", "zen", "thorium", "arc",
	} {
		b[n] = true
	}
	return &Kicker{Browsers: b, Port: 443}
}

func (k *Kicker) Kick(context.Context) error {
	errs := []error{winsys.FlushDNSCache()}
	procs, err := winsys.Processes()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	conns, err := winsys.TCPConns()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range conns {
		if c.Remote.Port() != k.Port {
			continue
		}
		p, ok := procs[c.PID]
		if !ok || !k.Browsers[strings.TrimSuffix(strings.ToLower(p.Name), ".exe")] {
			continue
		}
		if err := winsys.KillTCP(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
