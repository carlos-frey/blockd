package engine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Intervals of the background loops; variables so tests can shrink them.
var (
	ReconcileEvery = 15 * time.Second
	RefreshEvery   = 5 * time.Minute
	lookupTimeout  = 5 * time.Second
)

// reconcilerState lives inside Service (guarded by mu).
type reconcilerState struct {
	started    bool
	appliedNet string
	appliedDNS string
	// dnsDomains is what the installed sinkhole blocks, to spot new domains.
	dnsDomains []string
	// dynCache keeps the last dynHistoryLen resolutions of each ip_block domain.
	dynCache map[string][][]netip.Addr
}

func (s *Service) plans() (domain.Plan, backend.NetPlan, backend.DNSPlan) {
	cat := s.catalog(s.st)
	plan := domain.BuildPlan(cat, domain.Resolve(cat, s.st, s.d.Now()))
	net := backend.NetPlan{
		JailProcesses: len(plan.Processes) > 0,
		Dynamic:       len(plan.IPDomains) > 0,
		Static:        plan.Static,
	}
	return plan, net, backend.DNSPlan{Domains: plan.DNSDomains}
}

// Sync forces every backend to be re-applied and re-checked.
func (s *Service) Sync(ctx context.Context) (api.SyncView, error) {
	s.mu.Lock()
	err := s.reconcileLocked(ctx, true)
	v := s.sync
	s.mu.Unlock()
	s.requestRefresh()
	if err != nil {
		return v, domain.Errorf(domain.CodeBackend, "falha ao sincronizar: %v", err)
	}
	return v, nil
}

// reconcileLocked compares what each backend reports as installed with what
// the state asks for and re-applies on any difference. A difference that we
// did not cause ourselves is recorded as drift.
func (s *Service) reconcileLocked(ctx context.Context, force bool) error {
	plan, netPlan, dnsPlan := s.plans()
	r := &s.rec
	now := s.d.Now()
	s.sync.LastCheck = now
	var errs []error

	s.d.Procs.SetTargets(plan.Processes)

	// Network.
	wantNet := backend.Fingerprint(netPlan)
	gotNet, err := s.d.Network.Fingerprint(ctx)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("firewall: %w", err))
	case force || gotNet != wantNet:
		if r.started && gotNet != r.appliedNet {
			s.markDrift(now, "firewall", gotNet, r.appliedNet)
		}
		if netPlan.JailProcesses {
			if err := s.d.Procs.Prepare(); err != nil {
				errs = append(errs, fmt.Errorf("cgroup: %w", err))
				break
			}
		}
		if err := s.d.Network.Apply(ctx, netPlan); err != nil {
			errs = append(errs, fmt.Errorf("firewall: %w", err))
			break
		}
		r.appliedNet = wantNet
		// Sets start empty after Apply: refill from cache right away.
		if err := s.pushDynamicLocked(ctx, plan.IPDomains); err != nil {
			errs = append(errs, err)
		}
		if netPlan.Dynamic {
			s.requestRefresh()
		}
	}
	s.sync.Network = status(len(errs) == 0)

	// DNS.
	nErr := len(errs)
	wantDNS := backend.Fingerprint(dnsPlan)
	gotDNS, err := s.d.DNS.Fingerprint(ctx)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("dns: %w", err))
	case force || gotDNS != wantDNS:
		drift := r.started && gotDNS != r.appliedDNS
		if drift {
			s.markDrift(now, "dns", gotDNS, r.appliedDNS)
		}
		if err := s.d.DNS.Apply(ctx, dnsPlan); err != nil {
			errs = append(errs, fmt.Errorf("dns: %w", err))
			break
		}
		r.appliedDNS = wantDNS
		// Tabs opened before (or during a drift) would keep the domain alive.
		if drift || hasNew(dnsPlan.Domains, r.dnsDomains) {
			if err := s.d.Kicker.Kick(ctx); err != nil {
				s.d.Log.Warn("não consegui derrubar as sessões abertas dos navegadores", "err", err)
			}
		}
		r.dnsDomains = dnsPlan.Domains
	default:
		r.dnsDomains = dnsPlan.Domains
	}
	s.sync.DNS = status(len(errs) == nErr)

	r.started = true
	s.sync.InSync = len(errs) == 0
	s.sync.LastError = ""
	if err := errors.Join(errs...); err != nil {
		s.sync.LastError = err.Error()
		s.d.Log.Error("reconcile", "err", err)
		return err
	}
	return nil
}

// hasNew reports whether next has an entry missing from prev (both sorted).
func hasNew(next, prev []string) bool {
	for _, d := range next {
		if _, ok := slices.BinarySearch(prev, d); !ok {
			return true
		}
	}
	return false
}

func status(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func (s *Service) markDrift(now time.Time, what, got, want string) {
	t := now
	s.sync.LastDrift = &t
	s.d.Log.Warn("drift detectado; reaplicando", "backend", what, "instalado", got, "esperado", want)
}

// pushDynamicLocked loads the union of cached addresses of the given domains
// into the dynamic sets.
func (s *Service) pushDynamicLocked(ctx context.Context, domains []string) error {
	var addrs []netip.Addr
	for _, d := range domains {
		for _, gen := range s.rec.dynCache[d] {
			addrs = append(addrs, gen...)
		}
	}
	slices.SortFunc(addrs, netip.Addr.Compare)
	addrs = slices.Compact(addrs)
	if len(domains) == 0 {
		s.sync.DynamicIPs = 0 // the sets only exist while some domain needs them
		return nil
	}
	if err := s.d.Network.SetDynamic(ctx, addrs); err != nil {
		return fmt.Errorf("firewall (IPs dinâmicos): %w", err)
	}
	s.sync.DynamicIPs = len(addrs)
	return nil
}

func (s *Service) requestRefresh() {
	select {
	case s.refreshReq <- struct{}{}:
	default:
	}
}

// refreshDynamic resolves every ip_block domain without holding the lock,
// then updates the cache and the sets.
func (s *Service) refreshDynamic(ctx context.Context) {
	s.mu.Lock()
	plan, _, _ := s.plans()
	s.mu.Unlock()
	if len(plan.IPDomains) == 0 {
		return
	}
	fresh := map[string][]netip.Addr{}
	for _, d := range plan.IPDomains {
		lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		addrs, err := s.d.Resolver.LookupIPs(lctx, d)
		cancel()
		if err != nil {
			s.d.Log.Debug("lookup", "domain", d, "err", err)
			continue
		}
		fresh[d] = addrs
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec.dynCache == nil {
		s.rec.dynCache = map[string][][]netip.Addr{}
	}
	for d, addrs := range fresh {
		hist := append([][]netip.Addr{addrs}, s.rec.dynCache[d]...)
		if len(hist) > dynHistoryLen {
			hist = hist[:dynHistoryLen]
		}
		s.rec.dynCache[d] = hist
	}
	// The plan may have changed while we were resolving: use the current one.
	cur, _, _ := s.plans()
	if err := s.pushDynamicLocked(ctx, cur.IPDomains); err != nil {
		s.d.Log.Error("refresh", "err", err)
	}
}

// Run enforces the state at startup and keeps it enforced until ctx is done.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if err := s.reconcileLocked(ctx, false); err != nil {
		s.d.Log.Error("reconcile inicial falhou; tentando de novo no próximo ciclo", "err", err)
	}
	s.mu.Unlock()

	procErr := make(chan error, 1)
	go func() { procErr <- s.d.Procs.Run(ctx) }()

	reconcile := time.NewTicker(ReconcileEvery)
	defer reconcile.Stop()
	refresh := time.NewTicker(RefreshEvery)
	defer refresh.Stop()
	// The ticker alone would switch schedules up to ReconcileEvery late (and
	// is what catches up after a suspend); the timer makes it on the minute.
	change := time.NewTimer(s.untilNextChange())
	defer change.Stop()
	s.requestRefresh()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-procErr:
			return fmt.Errorf("process tracker: %w", err)
		case <-reconcile.C:
			s.tick(ctx)
		case <-change.C:
			s.tick(ctx)
			change.Reset(s.untilNextChange())
		case <-s.rearm:
			change.Reset(s.untilNextChange())
		case <-refresh.C:
			s.refreshDynamic(ctx)
		case <-s.refreshReq:
			s.refreshDynamic(ctx)
		}
	}
}

// tick drops ended focus sessions and pauses, then reconciles: a schedule
// that started or ended changes the plan like any mutation would.
func (s *Service) tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneState(s.st)
	if next.Prune(s.d.Now()) {
		next.Normalize()
		if err := s.d.Store.Save(next); err != nil {
			s.d.Log.Error("salvar estado podado", "err", err)
		} else {
			s.st = next
		}
	}
	_ = s.reconcileLocked(ctx, false)
}

// untilNextChange is how long Run waits before re-evaluating the schedules.
func (s *Service) untilNextChange() time.Duration {
	s.mu.Lock()
	now := s.d.Now()
	next := s.st.NextChange(now)
	s.mu.Unlock()
	if next == nil {
		return time.Hour
	}
	return min(max(next.Sub(now)+500*time.Millisecond, time.Second), time.Hour)
}

func (s *Service) requestRearm() {
	select {
	case s.rearm <- struct{}{}:
	default:
	}
}

// Teardown removes every enforcement artifact (rules, DNS redirect, jail)
// without touching the saved state. Next start re-applies it.
func (s *Service) Teardown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(
		s.d.Procs.Restore(ctx),
		s.d.Network.Teardown(ctx),
		s.d.DNS.Teardown(ctx),
	)
}
