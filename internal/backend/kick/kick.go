// Package kick makes running browsers forget what they knew about domains
// that just got blocked.
//
// The DNS sinkhole only affects new lookups. Browsers cache answers and keep
// HTTP/2 and QUIC sessions open for as long as a tab uses them, so a site
// that was open before the block keeps working indefinitely. Measured with
// Brave and Firefox against a local server in a network namespace:
//
//   - destroying the sockets alone is not enough: the browser reconnects
//     using its own DNS cache;
//   - a short-lived address on lo is a "network change": both browsers close
//     their HTTP/2 sessions and flush their host cache;
//   - Chromium keeps QUIC sessions across that change, so connected UDP
//     sockets to :443 are destroyed first.
//
// Every other connection is left alone; apps that use HTTP/3 just reconnect.
package kick

import (
	"context"
	"errors"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

type Kicker struct {
	Run sysexec.Runner
	// Addr is added to lo and removed right after. Must not collide with an
	// address in use (127.0.0.153 is the sinkhole).
	Addr string
	// Hold is how long Addr stays: browsers debounce netlink events, and an
	// add+del seen in the same batch may look like no change at all.
	Hold time.Duration
}

func New() *Kicker {
	return &Kicker{Run: sysexec.Exec{}, Addr: "127.0.0.154/32", Hold: 300 * time.Millisecond}
}

var _ backend.SessionKicker = (*Kicker)(nil)

func (k *Kicker) Kick(ctx context.Context) error {
	var errs []error
	if _, err := k.Run.Run(ctx, nil, "ss", "-K", "-n", "-u", "state", "established", "dport", "=", ":443"); err != nil {
		errs = append(errs, err)
	}
	// If a crash left Addr behind, add fails but del still makes the change.
	_, addErr := k.Run.Run(ctx, nil, "ip", "addr", "add", k.Addr, "dev", "lo")
	if addErr == nil {
		select {
		case <-ctx.Done():
		case <-time.After(k.Hold):
		}
	}
	if _, err := k.Run.Run(ctx, nil, "ip", "addr", "del", k.Addr, "dev", "lo"); err != nil {
		errs = append(errs, err)
		if addErr != nil {
			errs = append(errs, addErr)
		}
	}
	return errors.Join(errs...)
}
