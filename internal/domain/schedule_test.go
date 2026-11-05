package domain

import (
	"slices"
	"testing"
	"time"
)

var loc = time.FixedZone("BRT", -3*3600)

// at builds a time in loc; 2026-10-26 is a Monday.
func at(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, loc) }

func mustWindow(t *testing.T, s string) Window {
	t.Helper()
	w, err := ParseWindow(s)
	if err != nil {
		t.Fatalf("ParseWindow(%q): %v", s, err)
	}
	return w
}

func TestParseDays(t *testing.T) {
	cases := map[string][]int{
		"seg-sex":     {1, 2, 3, 4, 5},
		"sab,dom":     {0, 6},
		"sex-seg":     {0, 1, 5, 6},
		"uteis":       {1, 2, 3, 4, 5},
		"fds":         {0, 6},
		"todos":       {0, 1, 2, 3, 4, 5, 6},
		"qua, seg":    {1, 3},
		"sáb–dom":     {0, 6},
		"Seg-Qua,sex": {1, 2, 3, 5},
	}
	for in, want := range cases {
		got, err := ParseDays(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseDays(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "xyz", "seg-xyz"} {
		if _, err := ParseDays(bad); err == nil {
			t.Errorf("ParseDays(%q) should fail", bad)
		}
	}
}

func TestParseWindowAndLabel(t *testing.T) {
	w := mustWindow(t, "seg-sex 9h-18:30")
	if w.Start != "09:00" || w.End != "18:30" || !slices.Equal(w.Days, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("window = %+v", w)
	}
	if got := w.String(); got != "seg-sex 09:00-18:30" {
		t.Fatalf("String = %q", got)
	}
	if again := mustWindow(t, w.String()); !slices.Equal(again.Days, w.Days) || again.Start != w.Start {
		t.Fatalf("round trip = %+v", again)
	}
	if got := DaysLabel([]int{0, 6}); got != "sáb,dom" {
		t.Fatalf("DaysLabel(fds) = %q", got)
	}
	if got := DaysLabel([]int{1, 3, 4, 5}); got != "seg,qua-sex" {
		t.Fatalf("DaysLabel = %q", got)
	}
	if w := mustWindow(t, "22:00-02:00"); len(w.Days) != 7 {
		t.Fatalf("no days should mean every day, got %v", w.Days)
	}
	for _, bad := range []string{"seg-sex", "seg 25:00-26:00", "seg 9:7-10", "seg 24:00-02:00"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) should fail", bad)
		}
	}
}

func TestOccurrence(t *testing.T) {
	s := Schedule{Windows: []Window{mustWindow(t, "seg-sex 09:00-18:00")}}
	if o, ok := s.Current(at(26, 10, 0)); !ok || !o.Start.Equal(at(26, 9, 0)) || !o.End.Equal(at(26, 18, 0)) {
		t.Fatalf("monday 10:00 = %+v %v", o, ok)
	}
	if _, ok := s.Current(at(26, 18, 0)); ok {
		t.Fatal("end is exclusive")
	}
	if _, ok := s.Current(at(25, 10, 0)); ok {
		t.Fatal("sunday is off")
	}
	if o, ok := s.Next(at(26, 18, 0)); !ok || !o.Start.Equal(at(27, 9, 0)) {
		t.Fatalf("next after monday = %+v", o)
	}
	if o, ok := s.Next(at(30, 19, 0)); !ok || !o.Start.Equal(time.Date(2026, 11, 2, 9, 0, 0, 0, loc)) {
		t.Fatalf("next after friday = %+v", o)
	}
}

func TestOccurrenceCrossesMidnight(t *testing.T) {
	s := Schedule{Windows: []Window{mustWindow(t, "dom 23:00-01:00")}}
	o, ok := s.Current(at(26, 0, 30)) // monday 00:30, started sunday
	if !ok || !o.Start.Equal(at(25, 23, 0)) || !o.End.Equal(at(26, 1, 0)) {
		t.Fatalf("monday 00:30 = %+v %v", o, ok)
	}
	if _, ok := s.Current(at(26, 23, 30)); ok {
		t.Fatal("monday night is not in the schedule")
	}
}

func TestOccurrencesMerge(t *testing.T) {
	s := Schedule{Windows: []Window{mustWindow(t, "seg 09:00-12:00"), mustWindow(t, "seg 12:00-14:00"), mustWindow(t, "seg 13:00-15:00")}}
	if o, ok := s.Current(at(26, 10, 0)); !ok || !o.End.Equal(at(26, 15, 0)) {
		t.Fatalf("merged end = %+v", o)
	}
	all := Schedule{Windows: []Window{mustWindow(t, "todos 00:00-00:00")}}
	if o, ok := all.Current(at(26, 10, 0)); !ok || o.End.Sub(at(26, 10, 0)) < 7*24*time.Hour {
		t.Fatalf("24/7 should run far ahead, got %+v", o)
	}
}

func TestSkip(t *testing.T) {
	s := Schedule{Windows: []Window{mustWindow(t, "todos 22:00-02:00")}, Skip: []string{"2026-10-26"}}
	if _, ok := s.Current(at(26, 23, 0)); ok {
		t.Fatal("monday was skipped")
	}
	if _, ok := s.Current(at(27, 1, 0)); ok {
		t.Fatal("the skipped occurrence runs into tuesday and is skipped too")
	}
	if _, ok := s.Current(at(26, 1, 0)); !ok {
		t.Fatal("sunday's occurrence still runs into monday")
	}
}

func TestActiveSourcesPausesAndNextChange(t *testing.T) {
	s := Schedule{ID: "work", Name: "Trabalho", Enabled: true, Profiles: []string{"social"},
		Windows: []Window{mustWindow(t, "seg-sex 09:00-18:00")}}
	st := State{Schedules: []Schedule{s}}
	now := at(26, 10, 0)
	if src := st.ActiveSources(now); len(src) != 1 || src[0].ID != "schedule:work" || !src[0].Until.Equal(at(26, 18, 0)) {
		t.Fatalf("sources = %+v", src)
	}
	if nc := st.NextChange(now); nc == nil || !nc.Equal(at(26, 18, 0)) {
		t.Fatalf("next change = %v", nc)
	}

	st.Pauses = []Pause{{Source: "schedule:work", From: at(26, 10, 10), Until: at(26, 18, 0)}}
	if len(st.ActiveSources(now)) != 1 {
		t.Fatal("pending pause must keep blocking")
	}
	if nc := st.NextChange(now); !nc.Equal(at(26, 10, 10)) {
		t.Fatalf("next change should be the pause, got %v", nc)
	}
	if len(st.ActiveSources(at(26, 10, 10))) != 0 {
		t.Fatal("pause in effect")
	}
	if len(st.ActiveSources(at(27, 9, 0))) != 1 {
		t.Fatal("next day is back on")
	}

	st.Schedules[0].Enabled = false
	if len(st.ActiveSources(now)) != 0 || st.NextChange(now) == nil {
		t.Fatal("disabled schedule is off, pause still counts as a change")
	}
}

func TestPrune(t *testing.T) {
	st := State{
		Schedules: []Schedule{{ID: "work", Skip: []string{"2026-10-20", "2026-10-25", "2026-10-30"}}},
		Focus:     []FocusSession{{ID: "f1", Until: at(26, 9, 0)}, {ID: "f2", Until: at(26, 11, 0)}},
		Pauses: []Pause{
			{Source: "focus:f1", Until: at(26, 9, 0)},
			{Source: "schedule:work", Until: at(26, 18, 0)},
			{Source: "schedule:gone", Until: at(26, 18, 0)},
		},
	}
	if !st.Prune(at(26, 10, 0)) {
		t.Fatal("expected changes")
	}
	if len(st.Focus) != 1 || st.Focus[0].ID != "f2" || len(st.Pauses) != 1 || st.Pauses[0].Source != "schedule:work" {
		t.Fatalf("pruned = %+v %+v", st.Focus, st.Pauses)
	}
	if !slices.Equal(st.Schedules[0].Skip, []string{"2026-10-25", "2026-10-30"}) {
		t.Fatalf("skip = %v", st.Schedules[0].Skip)
	}
	if st.Prune(at(26, 10, 0)) {
		t.Fatal("second prune should be a no-op")
	}
}

func TestResolveWithSchedule(t *testing.T) {
	st := State{Schedules: []Schedule{{ID: "work", Name: "Trabalho", Enabled: true, Profiles: []string{"chat"},
		Windows: []Window{mustWindow(t, "seg 09:00-18:00")}}}}
	eff := Resolve(testCatalog(st), st, at(26, 10, 0))
	if got := eff["discord"]; !slices.Equal(got, []string{"schedule:work"}) {
		t.Fatalf("discord reasons = %v", got)
	}
	if got := eff.Holders("discord"); !slices.Equal(got, []string{"schedule:work"}) {
		t.Fatalf("holders = %v", got)
	}
	if eff := Resolve(testCatalog(st), st, at(26, 19, 0)); len(eff) != 0 {
		t.Fatalf("after hours = %v", eff)
	}
}

func TestGuard(t *testing.T) {
	g := Guard{Mode: GuardFriction}
	if err := g.Normalize(); err != nil || g.DelayMinutes != DefaultDelayMinutes || g.Phrase != DefaultPhrase {
		t.Fatalf("defaults = %+v %v", g, err)
	}
	if !g.PhraseMatches("  Eu escolho ABRIR mao   do meu foco agora ") {
		t.Fatal("phrase should match ignoring case and spaces")
	}
	locked := Guard{Mode: GuardLocked}
	free := Guard{Mode: GuardFree}
	shorter := Guard{Mode: GuardFriction, DelayMinutes: 5, Phrase: DefaultPhrase}
	if !locked.AtLeast(g) || g.AtLeast(locked) || free.AtLeast(g) || shorter.AtLeast(g) || !g.AtLeast(shorter) {
		t.Fatal("guard ordering is wrong")
	}
	if err := (&Guard{Mode: "nope"}).Normalize(); err == nil {
		t.Fatal("bad mode should fail")
	}
}
