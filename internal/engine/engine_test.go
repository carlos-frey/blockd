package engine

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/fake"
	"github.com/carlos-frey/blockd/internal/domain"
	"github.com/carlos-frey/blockd/internal/store"
)

type rig struct {
	svc   *Service
	net   *fake.Network
	dns   *fake.DNS
	procs *fake.Procs
	kick  *fake.Kicker
	store *store.Memory
}

func newRig(t *testing.T) rig {
	t.Helper()
	r := rig{net: &fake.Network{}, dns: &fake.DNS{}, procs: &fake.Procs{}, kick: &fake.Kicker{}, store: &store.Memory{}}
	svc, err := New(Deps{
		Store: r.store, Network: r.net, DNS: r.dns, Procs: r.procs, Kicker: r.kick,
		Resolver: fake.Resolver{"tiktok.com": {netip.MustParseAddr("203.0.113.7")}},
		BuiltinApps: []domain.App{
			{ID: "insta", Name: "Instagram", Category: "Social", Domains: []domain.Domain{{Name: "instagram.com"}}},
			{ID: "tiktok", Name: "TikTok", Category: "Social", Domains: []domain.Domain{{Name: "tiktok.com", IPBlock: true}}},
			{ID: "discord", Name: "Discord", Category: "Chat", Domains: []domain.Domain{{Name: "discord.com"}}, Processes: []string{"Discord"}},
			{ID: "doh", Name: "DoH", Category: "Sistema", IPs: []string{"1.1.1.1"}, Ports: []uint16{443}},
		},
		BuiltinProfiles: []domain.Profile{
			{ID: "social", Name: "Social", Apps: []string{"insta", "tiktok", "doh"}},
			{ID: "chat", Name: "Chat", Apps: []string{"discord", "doh"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

var ctx = context.Background()

func code(err error) string { return domain.AsError(err).Code }

func blockedIDs(v api.StatusView) []string {
	var ids []string
	for _, a := range v.Blocked {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestProfileOnEnforcesAndPersists(t *testing.T) {
	r := newRig(t)
	if err := r.svc.ProfileOn(ctx, []string{"social"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.dns.Plan.Domains, []string{"instagram.com", "tiktok.com"}) {
		t.Fatalf("dns plan = %v", r.dns.Plan.Domains)
	}
	if !r.net.Plan.Dynamic || r.net.Plan.JailProcesses || len(r.net.Plan.Static) != 1 {
		t.Fatalf("net plan = %+v", r.net.Plan)
	}
	if !slices.Equal(r.store.State.ActiveProfiles, []string{"social"}) {
		t.Fatalf("persisted = %+v", r.store.State)
	}
	st := r.svc.Status(ctx)
	if !st.Sync.InSync || !slices.Equal(blockedIDs(st), []string{"insta", "tiktok", "doh"}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnblockHeldByProfileFails(t *testing.T) {
	r := newRig(t)
	_ = r.svc.ProfileOn(ctx, []string{"social"})
	err := r.svc.Unblock(ctx, []string{"insta"})
	if code(err) != domain.CodeBlockedByProfile {
		t.Fatalf("err = %v", err)
	}
	if got := domain.AsError(err).Details["profiles"]; !slices.Equal(got.([]string), []string{"social"}) {
		t.Fatalf("details = %v", got)
	}
}

func TestManualSurvivesProfileOff(t *testing.T) {
	r := newRig(t)
	_ = r.svc.ProfileOn(ctx, []string{"social", "chat"})
	_ = r.svc.Block(ctx, []string{"insta"})
	if err := r.svc.ProfileOff(ctx, []string{"social"}); err != nil {
		t.Fatal(err)
	}
	got := blockedIDs(r.svc.Status(ctx))
	if !slices.Equal(got, []string{"insta", "discord", "doh"}) {
		t.Fatalf("blocked = %v (doh via chat, insta manual)", got)
	}
	if !slices.Equal(r.procs.Targets, []string{"Discord"}) || !r.net.Plan.JailProcesses {
		t.Fatalf("procs = %v, plan = %+v", r.procs.Targets, r.net.Plan)
	}
	if err := r.svc.Unblock(ctx, []string{"insta"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFromBlockedPreselects(t *testing.T) {
	r := newRig(t)
	_ = r.svc.ProfileOn(ctx, []string{"chat"})
	p, err := r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Meu Foco", Apps: []string{"insta"}, FromBlocked: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "meu-foco" || !slices.Equal(p.Apps, []string{"discord", "doh", "insta"}) || p.Builtin {
		t.Fatalf("profile = %+v", p)
	}
	if _, err := r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Meu foco"}); code(err) != domain.CodeAlreadyExists {
		t.Fatalf("dup err = %v", err)
	}
}

func TestBuiltinProfileIsReadonlyButClonable(t *testing.T) {
	r := newRig(t)
	name := "x"
	if _, err := r.svc.ProfileEdit(ctx, api.ProfileEditParams{ID: "social", Name: &name}); code(err) != domain.CodeBuiltinReadonly {
		t.Fatalf("err = %v", err)
	}
	c, err := r.svc.ProfileClone(ctx, api.ProfileCloneParams{ID: "social"})
	if err != nil || c.ID != "social-copia" {
		t.Fatalf("clone = %+v, %v", c, err)
	}
	c2, _ := r.svc.ProfileClone(ctx, api.ProfileCloneParams{ID: "social"})
	if c2.ID != "social-copia-2" {
		t.Fatalf("second clone id = %q", c2.ID)
	}
	edited, err := r.svc.ProfileEdit(ctx, api.ProfileEditParams{ID: c.ID, Remove: []string{"tiktok"}, Add: []string{"discord"}})
	if err != nil || !slices.Equal(edited.Apps, []string{"discord", "doh", "insta"}) {
		t.Fatalf("edit = %+v, %v", edited, err)
	}
}

func TestRemoveActiveUserProfileDeactivates(t *testing.T) {
	r := newRig(t)
	p, _ := r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Temp", Apps: []string{"insta"}, Activate: true})
	if len(r.dns.Plan.Domains) != 1 {
		t.Fatalf("expected insta enforced, dns = %v", r.dns.Plan.Domains)
	}
	if err := r.svc.ProfileRemove(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.store.State.ActiveProfiles) != 0 || len(r.dns.Plan.Domains) != 0 {
		t.Fatalf("state = %+v, dns = %v", r.store.State, r.dns.Plan.Domains)
	}
}

func TestCustomAppLifecycle(t *testing.T) {
	r := newRig(t)
	a, err := r.svc.CatalogAdd(ctx, domain.App{Name: "Bet Site", Domains: []domain.Domain{{Name: "bet.example"}}})
	if err != nil || a.ID != "bet-site" {
		t.Fatalf("add = %+v, %v", a, err)
	}
	_, _ = r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Bets", Apps: []string{"bet-site"}})
	_ = r.svc.Block(ctx, []string{"bet-site"})
	doms := []domain.Domain{{Name: "bet.example"}, {Name: "bet2.example"}}
	if _, err := r.svc.CatalogEdit(ctx, api.CatalogEditParams{ID: "bet-site", Domains: &doms}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.dns.Plan.Domains, []string{"bet.example", "bet2.example"}) {
		t.Fatalf("dns = %v", r.dns.Plan.Domains)
	}
	if err := r.svc.CatalogRemove(ctx, "insta"); code(err) != domain.CodeBuiltinReadonly {
		t.Fatalf("removing builtin: %v", err)
	}
	if err := r.svc.CatalogRemove(ctx, "bet-site"); err != nil {
		t.Fatal(err)
	}
	st := r.store.State
	if len(st.ManualApps) != 0 || len(st.UserProfiles[0].Apps) != 0 || len(r.dns.Plan.Domains) != 0 {
		t.Fatalf("dangling refs: %+v", st)
	}
}

func TestReconcileRepairsDrift(t *testing.T) {
	r := newRig(t)
	_ = r.svc.ProfileOn(ctx, []string{"social"})
	applies := r.net.Applies

	r.svc.mu.Lock()
	_ = r.svc.reconcileLocked(ctx, false)
	r.svc.mu.Unlock()
	if r.net.Applies != applies {
		t.Fatal("no-op reconcile should not re-apply")
	}

	r.net.Tamper()
	r.svc.mu.Lock()
	_ = r.svc.reconcileLocked(ctx, false)
	r.svc.mu.Unlock()
	if r.net.Applies != applies+1 || backend.Fingerprint(r.net.Plan) == "" {
		t.Fatalf("drift not repaired: applies=%d plan=%+v", r.net.Applies, r.net.Plan)
	}
	if r.svc.Status(ctx).Sync.LastDrift == nil {
		t.Fatal("drift should be recorded")
	}
}

func TestDynamicRefreshFillsSets(t *testing.T) {
	r := newRig(t)
	_ = r.svc.ProfileOn(ctx, []string{"social"})
	r.svc.refreshDynamic(ctx)
	if len(r.net.Dynamic) != 1 || r.net.Dynamic[0].String() != "203.0.113.7" {
		t.Fatalf("dynamic = %v", r.net.Dynamic)
	}
	// Re-applying (e.g. another profile turned on) must keep cached IPs.
	_ = r.svc.ProfileOn(ctx, []string{"chat"})
	if len(r.net.Dynamic) != 1 {
		t.Fatalf("dynamic lost after apply: %v", r.net.Dynamic)
	}
	_ = r.svc.ProfileOff(ctx, []string{"social", "chat"})
	if !r.net.Plan.Empty() || r.svc.Status(ctx).Sync.DynamicIPs != 0 {
		t.Fatalf("everything off should leave no rules: %+v", r.net.Plan)
	}
}

func TestFailedApplyKeepsStateAndReports(t *testing.T) {
	r := newRig(t)
	r.net.Err = domain.Errorf(domain.CodeInternal, "nft exploded")
	err := r.svc.ProfileOn(ctx, []string{"social"})
	if code(err) != domain.CodeBackend {
		t.Fatalf("err = %v", err)
	}
	st := r.svc.Status(ctx)
	if st.Sync.InSync || st.Sync.Network != "error" || len(r.store.State.ActiveProfiles) != 1 {
		t.Fatalf("status = %+v", st.Sync)
	}
	r.net.Err = nil
	if _, err := r.svc.Sync(ctx); err != nil || !r.svc.Status(ctx).Sync.InSync {
		t.Fatalf("sync after recovery: %v", err)
	}
}

func TestKickOnlyWhenDomainsAreAdded(t *testing.T) {
	r := newRig(t)
	step := func(what string, want int) {
		t.Helper()
		if got := r.kick.Count(); got != want {
			t.Fatalf("%s: kicks = %d, want %d", what, got, want)
		}
	}
	_ = r.svc.ProfileOn(ctx, []string{"social"})
	step("social on", 1)
	_ = r.svc.reconcileLocked(ctx, false)
	_, _ = r.svc.Sync(ctx)
	step("reconcile/sync sem mudança", 1)
	_ = r.svc.ProfileOn(ctx, []string{"chat"})
	step("chat on (discord.com novo)", 2)
	_ = r.svc.ProfileOff(ctx, []string{"chat"})
	step("chat off (só remove)", 2)
	_ = r.dns.Teardown(ctx) // alguém derrubou o sinkhole por fora
	_ = r.svc.reconcileLocked(ctx, false)
	step("drift reaplicado", 3)
}
