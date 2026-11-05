package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Longest focus session accepted, so a typo does not lock a week away.
const maxFocus = 24 * time.Hour

// ── Reads ──────────────────────────────────────────────────────────────

func (s *Service) ScheduleList(context.Context) []api.ScheduleView {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Now()
	out := []api.ScheduleView{}
	for _, sc := range s.st.Schedules {
		out = append(out, scheduleView(s.st, sc, now))
	}
	return out
}

func (s *Service) ScheduleShow(_ context.Context, id string) (api.ScheduleView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := scheduleIndex(&s.st, id)
	if i < 0 {
		return api.ScheduleView{}, notFoundSchedule(id)
	}
	return scheduleView(s.st, s.st.Schedules[i], s.d.Now()), nil
}

func scheduleView(st domain.State, sc domain.Schedule, now time.Time) api.ScheduleView {
	v := api.ScheduleView{Schedule: sc}
	if o, ok := sc.Current(now); ok {
		v.Current = &o
	}
	if o, ok := sc.Next(now); ok {
		v.Next = &o
	}
	if p, ok := st.PauseOf(domain.ScheduleSource(sc.ID)); ok {
		v.Pause = &p
	}
	v.Active = sc.Enabled && v.Current != nil && (v.Pause == nil || !v.Pause.InEffect(now))
	return v
}

func (s *Service) focusView(f domain.FocusSession, now time.Time) api.FocusView {
	v := api.FocusView{FocusSession: f}
	if p, ok := s.st.PauseOf(domain.FocusSource(f.ID)); ok {
		v.Pause = &p
	}
	v.Active = !now.Before(f.Start) && now.Before(f.Until) && (v.Pause == nil || !v.Pause.InEffect(now))
	return v
}

// ── Schedules ──────────────────────────────────────────────────────────

func (s *Service) ScheduleCreate(ctx context.Context, p api.ScheduleCreateParams) (api.ScheduleView, error) {
	var id string
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		id = p.ID
		if id == "" {
			id = domain.Slugify(p.Name)
		}
		if scheduleIndex(st, id) >= 0 {
			return domain.Errorf(domain.CodeAlreadyExists, "já existe uma agenda com id %q", id)
		}
		sc := domain.Schedule{
			ID: id, Name: p.Name, Profiles: slices.Clone(p.Profiles), Windows: slices.Clone(p.Windows),
			Enabled: !p.Disabled, Guard: p.Guard, Skip: []string{},
		}
		if err := validateSchedule(&sc, cat); err != nil {
			return err
		}
		st.Schedules = append(st.Schedules, sc)
		return nil
	})
	if err != nil {
		return api.ScheduleView{}, err
	}
	return s.ScheduleShow(ctx, id)
}

func (s *Service) ScheduleEdit(ctx context.Context, p api.ScheduleEditParams) (api.ScheduleView, error) {
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		i := scheduleIndex(st, p.ID)
		if i < 0 {
			return notFoundSchedule(p.ID)
		}
		sc := st.Schedules[i]
		set(&sc.Name, p.Name)
		set(&sc.Profiles, p.Profiles)
		set(&sc.Windows, p.Windows)
		set(&sc.Guard, p.Guard)
		if err := validateSchedule(&sc, cat); err != nil {
			return err
		}
		st.Schedules[i] = sc
		return nil
	})
	if err != nil {
		return api.ScheduleView{}, err
	}
	return s.ScheduleShow(ctx, p.ID)
}

func (s *Service) ScheduleRemove(ctx context.Context, id string) error {
	return s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		i := scheduleIndex(st, id)
		if i < 0 {
			return notFoundSchedule(id)
		}
		st.Schedules = slices.Delete(st.Schedules, i, i+1)
		return nil
	})
}

func (s *Service) ScheduleEnable(ctx context.Context, ids []string, on bool) error {
	return s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		for _, id := range ids {
			i := scheduleIndex(st, id)
			if i < 0 {
				return notFoundSchedule(id)
			}
			st.Schedules[i].Enabled = on
		}
		return nil
	})
}

// SchedulePause switches a schedule off until the end of the current
// occurrence, as its guard allows.
func (s *Service) SchedulePause(ctx context.Context, p api.PauseParams) (api.ScheduleView, error) {
	err := s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		i := scheduleIndex(st, p.ID)
		if i < 0 {
			return notFoundSchedule(p.ID)
		}
		sc := st.Schedules[i]
		now := s.d.Now()
		occ, ok := sc.Current(now)
		if !sc.Enabled || !ok {
			return domain.Errorf(domain.CodeInvalidArgument,
				"a agenda %s não está valendo agora; para pular um dia use `blockctl schedule skip %s <data>`", sc.Name, sc.ID)
		}
		src := domain.ScheduleSource(sc.ID)
		if done, err := pendingPause(*st, src, now); done || err != nil {
			return err
		}
		from, err := pauseStart(sc.Guard, p.Phrase, now, occ.End, "a agenda "+sc.Name)
		if err != nil {
			return err
		}
		st.Pauses = append(st.Pauses, domain.Pause{Source: src, From: from, Until: occ.End})
		return nil
	})
	if err != nil {
		return api.ScheduleView{}, err
	}
	return s.ScheduleShow(ctx, p.ID)
}

// ScheduleResume cancels a pause, pending or in effect. Always allowed: it
// only makes blocking stricter.
func (s *Service) ScheduleResume(ctx context.Context, id string) (api.ScheduleView, error) {
	err := s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		if scheduleIndex(st, id) < 0 {
			return notFoundSchedule(id)
		}
		dropPause(st, domain.ScheduleSource(id))
		return nil
	})
	if err != nil {
		return api.ScheduleView{}, err
	}
	return s.ScheduleShow(ctx, id)
}

// ScheduleSkip adds (or with Undo removes) a date whose occurrences do not
// happen. Skipping the occurrence in effect counts as switching it off, so
// the guard applies through checkLocks.
func (s *Service) ScheduleSkip(ctx context.Context, p api.ScheduleSkipParams) (api.ScheduleView, error) {
	err := s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		i := scheduleIndex(st, p.ID)
		if i < 0 {
			return notFoundSchedule(p.ID)
		}
		if _, err := time.Parse(domain.DateLayout, p.Date); err != nil {
			return domain.Errorf(domain.CodeInvalidArgument, "data inválida %q (use AAAA-MM-DD)", p.Date)
		}
		sc := &st.Schedules[i]
		if p.Undo {
			sc.Skip = slices.DeleteFunc(sc.Skip, func(d string) bool { return d == p.Date })
		} else {
			sc.Skip = append(sc.Skip, p.Date)
		}
		return nil
	})
	if err != nil {
		return api.ScheduleView{}, err
	}
	return s.ScheduleShow(ctx, p.ID)
}

// ── Focus sessions ─────────────────────────────────────────────────────

func (s *Service) FocusStart(ctx context.Context, p api.FocusStartParams) (api.FocusView, error) {
	var created domain.FocusSession
	err := s.mutate(ctx, func(st *domain.State, cat *domain.Catalog) error {
		now := s.d.Now()
		until, err := focusEnd(now, p.Minutes, p.Until)
		if err != nil {
			return err
		}
		profiles, err := checkProfiles(cat, p.Profiles)
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			return domain.Errorf(domain.CodeInvalidArgument, "escolha pelo menos um perfil para o foco")
		}
		g := p.Guard
		if err := g.Normalize(); err != nil {
			return err
		}
		created = domain.FocusSession{ID: nextFocusID(st), Profiles: profiles, Start: now, Until: until, Guard: g}
		st.Focus = append(st.Focus, created)
		return nil
	})
	if err != nil {
		return api.FocusView{}, err
	}
	return s.FocusShow(ctx, created.ID)
}

func (s *Service) FocusShow(_ context.Context, id string) (api.FocusView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := focusIndex(&s.st, id)
	if i < 0 {
		return api.FocusView{}, notFoundFocus(id)
	}
	return s.focusView(s.st.Focus[i], s.d.Now()), nil
}

// FocusStop ends a session right away (free) or after the friction wait.
func (s *Service) FocusStop(ctx context.Context, p api.PauseParams) error {
	return s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		i := focusIndex(st, p.ID)
		if i < 0 {
			return notFoundFocus(p.ID)
		}
		f := st.Focus[i]
		now := s.d.Now()
		src := domain.FocusSource(f.ID)
		if f.Guard.Mode == domain.GuardFree || now.Before(f.Start) {
			st.Focus = slices.Delete(st.Focus, i, i+1)
			return nil
		}
		if done, err := pendingPause(*st, src, now); done || err != nil {
			return err
		}
		from, err := pauseStart(f.Guard, p.Phrase, now, f.Until, "o foco")
		if err != nil {
			return err
		}
		st.Pauses = append(st.Pauses, domain.Pause{Source: src, From: from, Until: f.Until})
		return nil
	})
}

// FocusResume cancels a pending stop.
func (s *Service) FocusResume(ctx context.Context, id string) (api.FocusView, error) {
	err := s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		if focusIndex(st, id) < 0 {
			return notFoundFocus(id)
		}
		dropPause(st, domain.FocusSource(id))
		return nil
	})
	if err != nil {
		return api.FocusView{}, err
	}
	return s.FocusShow(ctx, id)
}

// FocusExtend pushes the end of a session (Minutes are added to it).
func (s *Service) FocusExtend(ctx context.Context, p api.FocusExtendParams) (api.FocusView, error) {
	err := s.mutate(ctx, func(st *domain.State, _ *domain.Catalog) error {
		i := focusIndex(st, p.ID)
		if i < 0 {
			return notFoundFocus(p.ID)
		}
		f := &st.Focus[i]
		until, err := focusEnd(f.Until, p.Minutes, p.Until)
		if err != nil {
			return err
		}
		if !until.After(f.Until) {
			return domain.Errorf(domain.CodeInvalidArgument, "o foco só pode ser estendido (termina às %s)", domain.When(f.Until, s.d.Now()))
		}
		if until.Sub(s.d.Now()) > maxFocus {
			return domain.Errorf(domain.CodeInvalidArgument, "um foco dura no máximo %s", maxFocus)
		}
		f.Until = until
		for j := range st.Pauses {
			if st.Pauses[j].Source == domain.FocusSource(f.ID) {
				st.Pauses[j].Until = until
			}
		}
		return nil
	})
	if err != nil {
		return api.FocusView{}, err
	}
	return s.FocusShow(ctx, p.ID)
}

// ── Locks ──────────────────────────────────────────────────────────────

// checkLocks rejects a state change that would loosen a locked or friction
// source that is blocking now: after the change it must still be active, end
// no earlier, keep a guard at least as strict and enforce at least the same
// plan. This covers editing its profiles or their apps, disabling, removing
// or skipping it, without listing every operation.
func (s *Service) checkLocks(before, after domain.State, now time.Time) error {
	locks := before.Locks(now)
	if len(locks) == 0 {
		return nil
	}
	bcat, acat := s.catalog(before), s.catalog(after)
	active := map[string]domain.Source{}
	for _, src := range after.ActiveSources(now) {
		active[src.ID] = src
	}
	for _, l := range locks {
		a, ok := active[l.ID]
		if ok && a.Guard.AtLeast(l.Guard) && !a.Until.Before(l.Until) && acat.SourcePlan(a).Covers(bcat.SourcePlan(l)) {
			continue
		}
		return lockedErr(l, now)
	}
	return nil
}

func lockedErr(l domain.Source, now time.Time) error {
	what, art := "a agenda "+l.Name, "a"
	cmd := "blockctl schedule pause " + strings.TrimPrefix(l.ID, "schedule:")
	if id, ok := strings.CutPrefix(l.ID, "focus:"); ok {
		what, art = "o foco", "o"
		cmd = "blockctl focus stop " + id
	}
	var msg string
	if l.Guard.Mode == domain.GuardLocked {
		msg = fmt.Sprintf("%s está travad%s até %s: nada que afrouxe o bloqueio vale antes disso", what, art, domain.When(l.Until, now))
	} else {
		msg = fmt.Sprintf("%s está valendo até %s e tem fricção: peça a pausa (%s) em vez de mexer nela", what, domain.When(l.Until, now), cmd)
	}
	return domain.Errorf(domain.CodeLocked, "%s", msg).
		WithDetails(map[string]any{"source": l.ID, "mode": l.Guard.Mode, "until": l.Until})
}

// pauseStart applies a guard to a pause request and returns when the pause
// takes effect.
func pauseStart(g domain.Guard, phrase string, now, until time.Time, what string) (time.Time, error) {
	switch g.Mode {
	case domain.GuardLocked:
		return time.Time{}, domain.Errorf(domain.CodeLocked, "não dá para pausar %s antes de %s: o modo é travado", what, domain.When(until, now)).
			WithDetails(map[string]any{"mode": g.Mode, "until": until})
	case domain.GuardFriction:
		if !g.PhraseMatches(phrase) {
			return time.Time{}, domain.Errorf(domain.CodeInvalidArgument, "frase incorreta; digite exatamente: %s", g.Phrase).
				WithDetails(map[string]any{"phrase": g.Phrase})
		}
		from := now.Add(time.Duration(g.DelayMinutes) * time.Minute)
		if !from.Before(until) {
			return time.Time{}, domain.Errorf(domain.CodeInvalidArgument,
				"%s termina às %s, antes de a espera de %d min acabar", what, domain.When(until, now), g.DelayMinutes)
		}
		return from, nil
	}
	return now, nil
}

// pendingPause reports done when the source is already paused, and an error
// when a pause was already requested and is still in its friction wait.
func pendingPause(st domain.State, src string, now time.Time) (done bool, err error) {
	p, ok := st.PauseOf(src)
	switch {
	case !ok:
		return false, nil
	case p.InEffect(now):
		return true, nil
	default:
		return true, domain.Errorf(domain.CodeAlreadyExists, "a pausa já foi pedida e começa às %s", domain.When(p.From, now))
	}
}

func dropPause(st *domain.State, src string) {
	st.Pauses = slices.DeleteFunc(st.Pauses, func(p domain.Pause) bool { return p.Source == src })
}

// ── helpers ────────────────────────────────────────────────────────────

// reasonLabel describes a BlockedBy reason for messages.
func (s *Service) reasonLabel(cat *domain.Catalog, st domain.State, r string) string {
	switch {
	case r == domain.ReasonManual:
		return "bloqueio avulso"
	case strings.HasPrefix(r, "profile:"):
		id := strings.TrimPrefix(r, "profile:")
		if p, ok := cat.Profile(id); ok {
			return "perfil " + p.Name
		}
		return "perfil " + id
	case strings.HasPrefix(r, "schedule:"):
		id := strings.TrimPrefix(r, "schedule:")
		if i := scheduleIndex(&st, id); i >= 0 {
			return "agenda " + st.Schedules[i].Name
		}
		return "agenda " + id
	case strings.HasPrefix(r, "focus:"):
		return "foco"
	}
	return r
}

func focusEnd(base time.Time, minutes int, until *time.Time) (time.Time, error) {
	switch {
	case until != nil:
		return *until, nil
	case minutes > 0:
		return base.Add(time.Duration(minutes) * time.Minute), nil
	}
	return time.Time{}, domain.Errorf(domain.CodeInvalidArgument, "diga quanto tempo (minutos) ou até quando")
}

func validateSchedule(sc *domain.Schedule, cat *domain.Catalog) error {
	if err := sc.Validate(); err != nil {
		return err
	}
	_, err := checkProfiles(cat, sc.Profiles)
	return err
}

func checkProfiles(cat *domain.Catalog, ids []string) ([]string, error) {
	for _, id := range ids {
		if _, ok := cat.Profile(id); !ok {
			return nil, notFoundProfile(id)
		}
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out), nil
}

func nextFocusID(st *domain.State) string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("f%d", n)
		if focusIndex(st, id) < 0 {
			return id
		}
	}
}

func scheduleIndex(st *domain.State, id string) int {
	return slices.IndexFunc(st.Schedules, func(s domain.Schedule) bool { return s.ID == id })
}

func focusIndex(st *domain.State, id string) int {
	return slices.IndexFunc(st.Focus, func(f domain.FocusSession) bool { return f.ID == id })
}

func notFoundSchedule(id string) error {
	return domain.Errorf(domain.CodeNotFound, "agenda %q não existe", id)
}

func notFoundFocus(id string) error {
	return domain.Errorf(domain.CodeNotFound, "foco %q não existe (veja blockctl focus ls)", id)
}
