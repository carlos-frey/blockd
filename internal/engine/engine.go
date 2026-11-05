// Package engine implements every use case on top of the backend interfaces
// and keeps the enforcement reconciled with the persisted state.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Deps are the injected collaborators.
type Deps struct {
	Store    backend.Store
	Network  backend.NetworkBlocker
	DNS      backend.DNSBlocker
	Procs    backend.ProcessTracker
	Resolver backend.Resolver
	Kicker   backend.SessionKicker // optional
	Log      *slog.Logger
	// Builtin catalog; injected so tests can use a tiny one.
	BuiltinApps     []domain.App
	BuiltinProfiles []domain.Profile
	DryRun          bool
	Now             func() time.Time
}

// Service is safe for concurrent use.
type Service struct {
	d  Deps
	mu sync.Mutex // guards st, sync and every backend mutation
	st domain.State

	sync       api.SyncView
	rec        reconcilerState
	refreshReq chan struct{}
	// rearm asks Run to recompute when the next scheduled change happens.
	rearm chan struct{}
}

// How many past resolutions stay in the dynamic sets, so a CDN that rotates
// addresses keeps the recently seen ones blocked too.
const dynHistoryLen = 3

func New(d Deps) (*Service, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Kicker == nil {
		d.Kicker = noKick{}
	}
	st, err := d.Store.Load()
	if err != nil {
		return nil, err
	}
	return &Service{d: d, st: st, refreshReq: make(chan struct{}, 1), rearm: make(chan struct{}, 1)}, nil
}

type noKick struct{}

func (noKick) Kick(context.Context) error { return nil }

// catalog builds the merged catalog for a given state.
func (s *Service) catalog(st domain.State) *domain.Catalog {
	return domain.NewCatalog(s.d.BuiltinApps, s.d.BuiltinProfiles, st)
}

// ── Reads ──────────────────────────────────────────────────────────────

func (s *Service) Status(context.Context) api.StatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Now()
	cat := s.catalog(s.st)
	eff := domain.Resolve(cat, s.st, now)
	v := api.StatusView{
		DryRun:         s.d.DryRun,
		ActiveProfiles: slices.Clone(s.st.ActiveProfiles),
		ManualApps:     slices.Clone(s.st.ManualApps),
		Blocked:        []api.AppView{},
		Plan:           domain.BuildPlan(cat, eff),
		Jailed:         s.d.Procs.Jailed(),
		Sync:           s.sync,

		ActiveSchedules: []string{},
		Focus:           []api.FocusView{},
		NextChange:      s.st.NextChange(now),
	}
	for _, src := range s.st.ActiveSources(now) {
		if id, ok := strings.CutPrefix(src.ID, "schedule:"); ok {
			v.ActiveSchedules = append(v.ActiveSchedules, id)
		}
	}
	for _, f := range s.st.Focus {
		v.Focus = append(v.Focus, s.focusView(f, now))
	}
	if v.Jailed == nil {
		v.Jailed = []backend.JailedProc{}
	}
	for _, a := range cat.Apps() {
		if _, ok := eff[a.ID]; ok {
			v.Blocked = append(v.Blocked, appView(cat, eff, a))
		}
	}
	return v
}

func (s *Service) CatalogList(_ context.Context, p api.CatalogListParams) []api.AppView {
	s.mu.Lock()
	defer s.mu.Unlock()
	cat := s.catalog(s.st)
	eff := domain.Resolve(cat, s.st, s.d.Now())
	q := strings.ToLower(strings.TrimSpace(p.Search))
	out := []api.AppView{}
	for _, a := range cat.Apps() {
		if p.Category != "" && !strings.EqualFold(a.Category, p.Category) {
			continue
		}
		if q != "" && !matches(a, q) {
			continue
		}
		out = append(out, appView(cat, eff, a))
	}
	return out
}

func matches(a domain.App, q string) bool {
	if strings.Contains(strings.ToLower(a.Name), q) || strings.Contains(a.ID, q) {
		return true
	}
	for _, d := range a.Domains {
		if strings.Contains(d.Name, q) {
			return true
		}
	}
	return false
}

func (s *Service) CatalogShow(_ context.Context, id string) (api.AppView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cat := s.catalog(s.st)
	a, ok := cat.App(id)
	if !ok {
		return api.AppView{}, notFoundApp(id)
	}
	return appView(cat, domain.Resolve(cat, s.st, s.d.Now()), a), nil
}

func (s *Service) ProfileList(context.Context) []api.ProfileView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []api.ProfileView{}
	for _, p := range s.catalog(s.st).Profiles() {
		out = append(out, s.profileView(p))
	}
	return out
}

func (s *Service) ProfileShow(_ context.Context, id string) (api.ProfileView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.catalog(s.st).Profile(id)
	if !ok {
		return api.ProfileView{}, notFoundProfile(id)
	}
	return s.profileView(p), nil
}

func (s *Service) profileView(p domain.Profile) api.ProfileView {
	v := api.ProfileView{Profile: p, Active: slices.Contains(s.st.ActiveProfiles, p.ID), ActiveBy: []string{}}
	for _, src := range s.st.ActiveSources(s.d.Now()) {
		if slices.Contains(src.Profiles, p.ID) {
			v.ActiveBy = append(v.ActiveBy, src.ID)
		}
	}
	return v
}

func appView(cat *domain.Catalog, eff domain.Effective, a domain.App) api.AppView {
	v := api.AppView{App: a, BlockedBy: []string{}, Profiles: []string{}}
	if r, ok := eff[a.ID]; ok {
		v.Blocked = true
		v.BlockedBy = slices.Clone(r)
	}
	for _, p := range cat.Profiles() {
		if slices.Contains(p.Apps, a.ID) {
			v.Profiles = append(v.Profiles, p.ID)
		}
	}
	return v
}

// ── Mutations ──────────────────────────────────────────────────────────

// mutate runs fn on a copy of the state; on success the copy is validated
// (including against active locks), persisted, made current and enforced.
func (s *Service) mutate(ctx context.Context, fn func(st *domain.State, cat *domain.Catalog) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Now()
	next := cloneState(s.st)
	if err := fn(&next, s.catalog(next)); err != nil {
		return err
	}
	next.Prune(now)
	next.Normalize()
	if err := s.checkLocks(s.st, next, now); err != nil {
		return err
	}
	if err := s.d.Store.Save(next); err != nil {
		return domain.Errorf(domain.CodeInternal, "falha ao salvar estado: %v", err)
	}
	s.st = next
	s.requestRearm()
	if err := s.reconcileLocked(ctx, false); err != nil {
		return domain.Errorf(domain.CodeBackend, "estado salvo, mas falhou ao aplicar: %v", err)
	}
	return nil
}

func (s *Service) Block(ctx context.Context, ids []string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		for _, id := range ids {
			if _, ok := cat.App(id); !ok {
				return notFoundApp(id)
			}
			st.ManualApps = append(st.ManualApps, id)
		}
		return nil
	})
}

// Unblock removes the manual block. Apps held by an active profile cannot be
// unblocked individually; the error lists the profiles holding them.
func (s *Service) Unblock(ctx context.Context, ids []string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		eff := domain.Resolve(cat, *st, s.d.Now())
		for _, id := range ids {
			if _, ok := cat.App(id); !ok {
				return notFoundApp(id)
			}
			if held := eff.Holders(id); len(held) > 0 {
				a, _ := cat.App(id)
				var labels []string
				for _, r := range held {
					labels = append(labels, s.reasonLabel(cat, *st, r))
				}
				return domain.Errorf(domain.CodeBlockedByProfile,
					"%s está bloqueado por %s; desligue isso para liberar",
					a.Name, strings.Join(labels, ", ")).
					WithDetails(map[string]any{"app": id, "profiles": eff.HoldingProfiles(id), "held_by": held})
			}
		}
		st.ManualApps = slices.DeleteFunc(st.ManualApps, func(m string) bool { return slices.Contains(ids, m) })
		return nil
	})
}

func (s *Service) ProfileOn(ctx context.Context, ids []string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		for _, id := range ids {
			if _, ok := cat.Profile(id); !ok {
				return notFoundProfile(id)
			}
			st.ActiveProfiles = append(st.ActiveProfiles, id)
		}
		return nil
	})
}

func (s *Service) ProfileOff(ctx context.Context, ids []string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		for _, id := range ids {
			if _, ok := cat.Profile(id); !ok {
				return notFoundProfile(id)
			}
		}
		st.ActiveProfiles = slices.DeleteFunc(st.ActiveProfiles, func(a string) bool { return slices.Contains(ids, a) })
		return nil
	})
}

func (s *Service) ProfileCreate(ctx context.Context, p api.ProfileCreateParams) (api.ProfileView, error) {
	var created domain.Profile
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return domain.Errorf(domain.CodeInvalidArgument, "o perfil precisa de um nome")
		}
		id := p.ID
		if id == "" {
			id = domain.Slugify(name)
		}
		if !domain.ValidID(id) {
			return domain.Errorf(domain.CodeInvalidArgument, "id de perfil inválido %q", id)
		}
		if _, exists := cat.Profile(id); exists {
			return domain.Errorf(domain.CodeAlreadyExists, "já existe um perfil com id %q", id)
		}
		apps := slices.Clone(p.Apps)
		if p.FromBlocked {
			apps = append(apps, domain.Resolve(cat, *st, s.d.Now()).IDs()...)
		}
		apps, err := checkApps(cat, apps)
		if err != nil {
			return err
		}
		created = domain.Profile{ID: id, Name: name, Description: strings.TrimSpace(p.Description), Apps: apps}
		st.UserProfiles = append(st.UserProfiles, created)
		if p.Activate {
			st.ActiveProfiles = append(st.ActiveProfiles, id)
		}
		return nil
	})
	if err != nil {
		return api.ProfileView{}, err
	}
	return s.ProfileShow(ctx, created.ID)
}

func (s *Service) ProfileEdit(ctx context.Context, p api.ProfileEditParams) (api.ProfileView, error) {
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		i, err := userProfileIndex(st, cat, p.ID)
		if err != nil {
			return err
		}
		prof := &st.UserProfiles[i]
		if p.Name != nil {
			if strings.TrimSpace(*p.Name) == "" {
				return domain.Errorf(domain.CodeInvalidArgument, "o perfil precisa de um nome")
			}
			prof.Name = strings.TrimSpace(*p.Name)
		}
		if p.Description != nil {
			prof.Description = strings.TrimSpace(*p.Description)
		}
		apps := slices.Clone(prof.Apps)
		if p.SetApps != nil {
			apps = slices.Clone(*p.SetApps)
		}
		apps = append(apps, p.Add...)
		apps = slices.DeleteFunc(apps, func(a string) bool { return slices.Contains(p.Remove, a) })
		prof.Apps, err = checkApps(cat, apps)
		return err
	})
	if err != nil {
		return api.ProfileView{}, err
	}
	return s.ProfileShow(ctx, p.ID)
}

func (s *Service) ProfileClone(ctx context.Context, p api.ProfileCloneParams) (api.ProfileView, error) {
	var id string
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		src, ok := cat.Profile(p.ID)
		if !ok {
			return notFoundProfile(p.ID)
		}
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = src.Name + " (cópia)"
		}
		base := domain.Slugify(name)
		id = base
		for n := 2; ; n++ {
			if _, exists := cat.Profile(id); !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", base, n)
		}
		st.UserProfiles = append(st.UserProfiles, domain.Profile{
			ID: id, Name: name, Description: src.Description, Apps: slices.Clone(src.Apps),
		})
		return nil
	})
	if err != nil {
		return api.ProfileView{}, err
	}
	return s.ProfileShow(ctx, id)
}

func (s *Service) ProfileRemove(ctx context.Context, id string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		i, err := userProfileIndex(st, cat, id)
		if err != nil {
			return err
		}
		st.UserProfiles = slices.Delete(st.UserProfiles, i, i+1)
		st.ActiveProfiles = slices.DeleteFunc(st.ActiveProfiles, func(a string) bool { return a == id })
		drop := func(p string) bool { return p == id }
		for j := range st.Schedules {
			st.Schedules[j].Profiles = slices.DeleteFunc(st.Schedules[j].Profiles, drop)
		}
		for j := range st.Focus {
			st.Focus[j].Profiles = slices.DeleteFunc(st.Focus[j].Profiles, drop)
		}
		return nil
	})
}

func (s *Service) CatalogAdd(ctx context.Context, a domain.App) (api.AppView, error) {
	if a.ID == "" {
		a.ID = domain.Slugify(a.Name)
	}
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		a.Builtin = false
		if err := a.Validate(); err != nil {
			return err
		}
		if _, exists := cat.App(a.ID); exists {
			return domain.Errorf(domain.CodeAlreadyExists, "já existe um app com id %q", a.ID)
		}
		st.CustomApps = append(st.CustomApps, a)
		return nil
	})
	if err != nil {
		return api.AppView{}, err
	}
	return s.CatalogShow(ctx, a.ID)
}

func (s *Service) CatalogEdit(ctx context.Context, p api.CatalogEditParams) (api.AppView, error) {
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		i, err := customAppIndex(st, cat, p.ID)
		if err != nil {
			return err
		}
		a := st.CustomApps[i]
		set(&a.Name, p.Name)
		set(&a.Category, p.Category)
		set(&a.Description, p.Description)
		set(&a.Domains, p.Domains)
		set(&a.Processes, p.Processes)
		set(&a.IPs, p.IPs)
		set(&a.Ports, p.Ports)
		if err := a.Validate(); err != nil {
			return err
		}
		st.CustomApps[i] = a
		return nil
	})
	if err != nil {
		return api.AppView{}, err
	}
	return s.CatalogShow(ctx, p.ID)
}

// CatalogRemove deletes a custom app and every reference to it.
func (s *Service) CatalogRemove(ctx context.Context, id string) error {
	return s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		i, err := customAppIndex(st, cat, id)
		if err != nil {
			return err
		}
		st.CustomApps = slices.Delete(st.CustomApps, i, i+1)
		st.ManualApps = slices.DeleteFunc(st.ManualApps, func(m string) bool { return m == id })
		for j := range st.UserProfiles {
			st.UserProfiles[j].Apps = slices.DeleteFunc(st.UserProfiles[j].Apps, func(m string) bool { return m == id })
		}
		return nil
	})
}

// ── helpers ────────────────────────────────────────────────────────────

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func checkApps(cat *domain.Catalog, apps []string) ([]string, error) {
	for _, a := range apps {
		if _, ok := cat.App(a); !ok {
			return nil, notFoundApp(a)
		}
	}
	slices.Sort(apps)
	apps = slices.Compact(apps)
	if apps == nil {
		apps = []string{}
	}
	return apps, nil
}

func userProfileIndex(st *domain.State, cat *domain.Catalog, id string) (int, error) {
	p, ok := cat.Profile(id)
	if !ok {
		return -1, notFoundProfile(id)
	}
	if p.Builtin {
		return -1, domain.Errorf(domain.CodeBuiltinReadonly,
			"o perfil %q é pronto e não pode ser alterado; use `profile clone %s`", id, id)
	}
	return slices.IndexFunc(st.UserProfiles, func(p domain.Profile) bool { return p.ID == id }), nil
}

func customAppIndex(st *domain.State, cat *domain.Catalog, id string) (int, error) {
	a, ok := cat.App(id)
	if !ok {
		return -1, notFoundApp(id)
	}
	if a.Builtin {
		return -1, domain.Errorf(domain.CodeBuiltinReadonly, "o app %q é do catálogo embutido e não pode ser alterado", id)
	}
	return slices.IndexFunc(st.CustomApps, func(a domain.App) bool { return a.ID == id }), nil
}

func notFoundApp(id string) error {
	return domain.Errorf(domain.CodeNotFound, "app %q não existe no catálogo", id)
}

func notFoundProfile(id string) error {
	return domain.Errorf(domain.CodeNotFound, "perfil %q não existe", id)
}

func cloneState(st domain.State) domain.State {
	out := st
	out.ActiveProfiles = slices.Clone(st.ActiveProfiles)
	out.ManualApps = slices.Clone(st.ManualApps)
	out.CustomApps = make([]domain.App, len(st.CustomApps))
	for i, a := range st.CustomApps {
		a.Domains = slices.Clone(a.Domains)
		a.Processes = slices.Clone(a.Processes)
		a.IPs = slices.Clone(a.IPs)
		a.Ports = slices.Clone(a.Ports)
		out.CustomApps[i] = a
	}
	out.UserProfiles = make([]domain.Profile, len(st.UserProfiles))
	for i, p := range st.UserProfiles {
		p.Apps = slices.Clone(p.Apps)
		out.UserProfiles[i] = p
	}
	out.Schedules = make([]domain.Schedule, len(st.Schedules))
	for i, sc := range st.Schedules {
		sc.Profiles = slices.Clone(sc.Profiles)
		sc.Skip = slices.Clone(sc.Skip)
		sc.Windows = slices.Clone(sc.Windows)
		for j := range sc.Windows {
			sc.Windows[j].Days = slices.Clone(sc.Windows[j].Days)
		}
		out.Schedules[i] = sc
	}
	out.Focus = make([]domain.FocusSession, len(st.Focus))
	for i, f := range st.Focus {
		f.Profiles = slices.Clone(f.Profiles)
		out.Focus[i] = f
	}
	out.Pauses = slices.Clone(st.Pauses)
	return out
}
