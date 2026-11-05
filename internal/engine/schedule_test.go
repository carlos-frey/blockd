package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// clock is a settable Deps.Now. 2026-10-26 is a Monday.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) set(day, hh, mm int) { c.t = time.Date(2026, 10, day, hh, mm, 0, 0, time.Local) }

func newClockRig(t *testing.T, day, hh, mm int) (rig, *clock) {
	t.Helper()
	r := newRig(t)
	c := &clock{}
	c.set(day, hh, mm)
	r.svc.d.Now = c.now
	return r, c
}

func windows(t *testing.T, specs ...string) []domain.Window {
	t.Helper()
	var out []domain.Window
	for _, s := range specs {
		w, err := domain.ParseWindow(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, w)
	}
	return out
}

func mustSchedule(t *testing.T, r rig, name string, profiles []string, g domain.Guard, specs ...string) api.ScheduleView {
	t.Helper()
	v, err := r.svc.ScheduleCreate(ctx, api.ScheduleCreateParams{Name: name, Profiles: profiles, Windows: windows(t, specs...), Guard: g})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func blocks(r rig, domain string) bool { return slices.Contains(r.dns.Plan.Domains, domain) }

func TestScheduleSwitchesOnAndOffWithTheClock(t *testing.T) {
	r, c := newClockRig(t, 26, 8, 59)
	v := mustSchedule(t, r, "Trabalho", []string{"chat"}, domain.Guard{}, "seg-sex 09:00-18:00")
	if v.ID != "trabalho" || v.Active || v.Next == nil {
		t.Fatalf("view = %+v", v)
	}
	if blocks(r, "discord.com") {
		t.Fatal("not yet 09:00")
	}
	c.set(26, 9, 0)
	r.svc.tick(ctx)
	if !blocks(r, "discord.com") || !slices.Equal(r.procs.Targets, []string{"Discord"}) {
		t.Fatalf("09:00 should block, dns = %v", r.dns.Plan.Domains)
	}
	st := r.svc.Status(ctx)
	if !slices.Equal(st.ActiveSchedules, []string{"trabalho"}) || st.NextChange == nil || st.NextChange.Hour() != 18 {
		t.Fatalf("status = %+v", st)
	}
	if got := st.Blocked[0].BlockedBy; !slices.Equal(got, []string{"schedule:trabalho"}) {
		t.Fatalf("blocked by = %v", got)
	}
	if pv, _ := r.svc.ProfileShow(ctx, "chat"); pv.Active || !slices.Equal(pv.ActiveBy, []string{"schedule:trabalho"}) {
		t.Fatalf("profile view = %+v", pv)
	}
	if err := r.svc.Unblock(ctx, []string{"discord"}); code(err) != domain.CodeBlockedByProfile {
		t.Fatalf("unblock = %v", err)
	}
	c.set(26, 18, 0)
	r.svc.tick(ctx)
	if blocks(r, "discord.com") {
		t.Fatal("18:00 should release")
	}
}

func TestLockedScheduleRejectsLoosening(t *testing.T) {
	r, _ := newClockRig(t, 26, 10, 0)
	if _, err := r.svc.CatalogAdd(ctx, domain.App{Name: "Bet", Domains: []domain.Domain{{Name: "bet.com"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Meu", Apps: []string{"insta", "bet"}}); err != nil {
		t.Fatal(err)
	}
	mustSchedule(t, r, "Manhã", []string{"meu"}, domain.Guard{Mode: domain.GuardLocked}, "seg 09:00-12:00")
	if !blocks(r, "bet.com") {
		t.Fatal("schedule should be blocking")
	}

	other := windows(t, "seg 09:00-11:00")
	free := domain.Guard{Mode: domain.GuardFree}
	tomorrowSkip := api.ScheduleSkipParams{ID: "manha", Date: "2026-10-27"}
	denied := map[string]func() error{
		"remove app from profile": func() error {
			_, err := r.svc.ProfileEdit(ctx, api.ProfileEditParams{ID: "meu", Remove: []string{"insta"}})
			return err
		},
		"remove profile":  func() error { return r.svc.ProfileRemove(ctx, "meu") },
		"disable":         func() error { return r.svc.ScheduleEnable(ctx, []string{"manha"}, false) },
		"remove schedule": func() error { return r.svc.ScheduleRemove(ctx, "manha") },
		"end earlier": func() error {
			_, err := r.svc.ScheduleEdit(ctx, api.ScheduleEditParams{ID: "manha", Windows: &other})
			return err
		},
		"weaker guard": func() error {
			_, err := r.svc.ScheduleEdit(ctx, api.ScheduleEditParams{ID: "manha", Guard: &free})
			return err
		},
		"skip today": func() error {
			_, err := r.svc.ScheduleSkip(ctx, api.ScheduleSkipParams{ID: "manha", Date: "2026-10-26"})
			return err
		},
		"pause": func() error {
			_, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "manha"})
			return err
		},
		"change custom app domains": func() error {
			d := []domain.Domain{{Name: "other.com"}}
			_, err := r.svc.CatalogEdit(ctx, api.CatalogEditParams{ID: "bet", Domains: &d})
			return err
		},
		"remove custom app": func() error { return r.svc.CatalogRemove(ctx, "bet") },
	}
	for name, fn := range denied {
		if err := fn(); code(err) != domain.CodeLocked {
			t.Errorf("%s: err = %v, want locked", name, err)
		}
	}
	if !blocks(r, "bet.com") || !blocks(r, "instagram.com") {
		t.Fatal("nothing should have been released")
	}

	longer := windows(t, "seg 09:00-20:00")
	allowed := map[string]func() error{
		"add app": func() error {
			_, err := r.svc.ProfileEdit(ctx, api.ProfileEditParams{ID: "meu", Add: []string{"tiktok"}})
			return err
		},
		"skip tomorrow": func() error { _, err := r.svc.ScheduleSkip(ctx, tomorrowSkip); return err },
		"end later": func() error {
			_, err := r.svc.ScheduleEdit(ctx, api.ScheduleEditParams{ID: "manha", Windows: &longer})
			return err
		},
		"unrelated profile off": func() error { return r.svc.ProfileOff(ctx, []string{"social"}) },
	}
	for name, fn := range allowed {
		if err := fn(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFrictionPauseWaitsAndCanBeCancelled(t *testing.T) {
	r, c := newClockRig(t, 26, 10, 0)
	g := domain.Guard{Mode: domain.GuardFriction, DelayMinutes: 10, Phrase: "quero sair"}
	mustSchedule(t, r, "Trabalho", []string{"chat"}, g, "seg-sex 09:00-18:00")

	if _, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "trabalho", Phrase: "outra"}); code(err) != domain.CodeInvalidArgument {
		t.Fatalf("wrong phrase = %v", err)
	}
	if err := r.svc.ScheduleEnable(ctx, []string{"trabalho"}, false); code(err) != domain.CodeLocked {
		t.Fatalf("disable during friction = %v", err)
	}
	v, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "trabalho", Phrase: " Quero  SAIR "})
	if err != nil {
		t.Fatal(err)
	}
	if v.Pause == nil || v.Pause.From.Sub(c.t) != 10*time.Minute || !v.Active {
		t.Fatalf("view = %+v", v)
	}
	if _, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "trabalho", Phrase: "quero sair"}); code(err) != domain.CodeAlreadyExists {
		t.Fatalf("second pause = %v", err)
	}
	c.set(26, 10, 9)
	r.svc.tick(ctx)
	if !blocks(r, "discord.com") {
		t.Fatal("still waiting")
	}
	c.set(26, 10, 10)
	r.svc.tick(ctx)
	if blocks(r, "discord.com") {
		t.Fatal("pause should be in effect")
	}
	// While paused it is not a lock anymore.
	if err := r.svc.ScheduleEnable(ctx, []string{"trabalho"}, false); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ScheduleEnable(ctx, []string{"trabalho"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ScheduleResume(ctx, "trabalho"); err != nil {
		t.Fatal(err)
	}
	if !blocks(r, "discord.com") {
		t.Fatal("resume should block again")
	}
}

func TestFreePauseLastsUntilTheEndOfTheWindow(t *testing.T) {
	r, c := newClockRig(t, 26, 10, 0)
	mustSchedule(t, r, "Trabalho", []string{"chat"}, domain.Guard{}, "seg-sex 09:00-18:00")
	if _, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "trabalho"}); err != nil {
		t.Fatal(err)
	}
	if blocks(r, "discord.com") {
		t.Fatal("free pause is immediate")
	}
	c.set(27, 9, 0)
	r.svc.tick(ctx)
	if !blocks(r, "discord.com") || len(r.store.State.Pauses) != 0 {
		t.Fatalf("next day should block again and prune the pause: %+v", r.store.State.Pauses)
	}
	c.set(27, 20, 0)
	if _, err := r.svc.SchedulePause(ctx, api.PauseParams{ID: "trabalho"}); code(err) != domain.CodeInvalidArgument {
		t.Fatalf("pause outside the window = %v", err)
	}
}

func TestFocusSessions(t *testing.T) {
	r, c := newClockRig(t, 26, 10, 0)
	f, err := r.svc.FocusStart(ctx, api.FocusStartParams{Profiles: []string{"chat"}, Minutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Active || f.ID != "f1" || !blocks(r, "discord.com") {
		t.Fatalf("focus = %+v", f)
	}
	if err := r.svc.FocusStop(ctx, api.PauseParams{ID: "f1"}); err != nil || blocks(r, "discord.com") || len(r.store.State.Focus) != 0 {
		t.Fatalf("free stop: %v, focus = %+v", err, r.store.State.Focus)
	}

	locked, err := r.svc.FocusStart(ctx, api.FocusStartParams{Profiles: []string{"social"}, Minutes: 30, Guard: domain.Guard{Mode: domain.GuardLocked}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.FocusStop(ctx, api.PauseParams{ID: locked.ID}); code(err) != domain.CodeLocked {
		t.Fatalf("locked stop = %v", err)
	}
	if _, err := r.svc.FocusExtend(ctx, api.FocusExtendParams{ID: locked.ID, Minutes: -10}); err == nil {
		t.Fatal("shortening must fail")
	}
	ext, err := r.svc.FocusExtend(ctx, api.FocusExtendParams{ID: locked.ID, Minutes: 15})
	if err != nil || ext.Until.Sub(c.t) != 45*time.Minute {
		t.Fatalf("extend = %+v %v", ext, err)
	}
	c.set(26, 10, 45)
	r.svc.tick(ctx)
	if blocks(r, "instagram.com") || len(r.store.State.Focus) != 0 {
		t.Fatalf("ended focus should be released and pruned: %+v", r.store.State.Focus)
	}
	if _, err := r.svc.FocusStart(ctx, api.FocusStartParams{Profiles: []string{"chat"}}); code(err) != domain.CodeInvalidArgument {
		t.Fatalf("no duration = %v", err)
	}
}

func TestRemovingProfileDropsItFromSchedules(t *testing.T) {
	r, c := newClockRig(t, 26, 20, 0)
	if _, err := r.svc.ProfileCreate(ctx, api.ProfileCreateParams{Name: "Meu", Apps: []string{"insta"}}); err != nil {
		t.Fatal(err)
	}
	mustSchedule(t, r, "Noite", []string{"meu", "chat"}, domain.Guard{Mode: domain.GuardLocked}, "seg 21:00-23:00")
	if err := r.svc.ProfileRemove(ctx, "meu"); err != nil {
		t.Fatal(err)
	}
	if got := r.store.State.Schedules[0].Profiles; !slices.Equal(got, []string{"chat"}) {
		t.Fatalf("profiles = %v", got)
	}
	c.set(26, 21, 0)
	r.svc.tick(ctx)
	if !blocks(r, "discord.com") {
		t.Fatal("the remaining profile still runs")
	}
}
