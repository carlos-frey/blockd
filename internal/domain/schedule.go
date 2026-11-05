package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GuardMode is how hard it is to switch off a schedule or focus session while
// it is blocking.
type GuardMode string

const (
	GuardFree     GuardMode = "free"     // pause right away
	GuardFriction GuardMode = "friction" // type the phrase, then wait DelayMinutes
	GuardLocked   GuardMode = "locked"   // no way out until it ends
)

const (
	DefaultDelayMinutes = 10
	DefaultPhrase       = "eu escolho abrir mao do meu foco agora"
	maxDelayMinutes     = 24 * 60
)

// Guard is attached to every schedule and focus session.
type Guard struct {
	Mode         GuardMode `json:"mode"`
	DelayMinutes int       `json:"delay_minutes,omitempty"`
	Phrase       string    `json:"phrase,omitempty"`
}

// Normalize fills defaults and drops fields that do not apply to the mode.
func (g *Guard) Normalize() error {
	switch g.Mode {
	case "":
		g.Mode = GuardFree
	case GuardFree, GuardFriction, GuardLocked:
	default:
		return Errorf(CodeInvalidArgument, "modo %q inválido (use free, friction ou locked)", g.Mode)
	}
	if g.Mode != GuardFriction {
		g.DelayMinutes, g.Phrase = 0, ""
		return nil
	}
	if g.DelayMinutes <= 0 {
		g.DelayMinutes = DefaultDelayMinutes
	}
	if g.DelayMinutes > maxDelayMinutes {
		return Errorf(CodeInvalidArgument, "a espera da fricção vai até %d minutos", maxDelayMinutes)
	}
	g.Phrase = normPhrase(g.Phrase)
	if g.Phrase == "" {
		g.Phrase = DefaultPhrase
	}
	return nil
}

// PhraseMatches compares ignoring case and repeated spaces.
func (g Guard) PhraseMatches(s string) bool { return normPhrase(s) == normPhrase(g.Phrase) }

func normPhrase(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func (g Guard) rank() int {
	switch g.Mode {
	case GuardLocked:
		return 2
	case GuardFriction:
		return 1
	}
	return 0
}

// AtLeast reports whether g is as strict as o: same mode with no shorter wait,
// or a stricter mode.
func (g Guard) AtLeast(o Guard) bool {
	if g.rank() != o.rank() {
		return g.rank() > o.rank()
	}
	return g.Mode != GuardFriction || (g.DelayMinutes >= o.DelayMinutes && normPhrase(g.Phrase) == normPhrase(o.Phrase))
}

// Window is a weekly time range. Days are the weekdays it starts on
// (0 = domingo … 6 = sábado). An End not after Start crosses midnight, so
// 22:00–02:00 ends the next day and 00:00–00:00 lasts 24h.
type Window struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// span returns the start in minutes since midnight and the duration.
func (w Window) span() (start, dur int) {
	start, _ = parseClock(w.Start)
	end, _ := parseClock(w.End)
	dur = end - start
	if dur <= 0 {
		dur += 24 * 60
	}
	return start, dur
}

func (w *Window) Validate() error {
	if len(w.Days) == 0 {
		return Errorf(CodeInvalidArgument, "janela %s–%s sem dias da semana", w.Start, w.End)
	}
	for _, d := range w.Days {
		if d < 0 || d > 6 {
			return Errorf(CodeInvalidArgument, "dia da semana inválido %d (0 = domingo … 6 = sábado)", d)
		}
	}
	w.Days = sortedInts(w.Days)
	s, err := parseClock(w.Start)
	if err != nil || s >= 24*60 {
		return Errorf(CodeInvalidArgument, "horário de início inválido %q", w.Start)
	}
	e, err := parseClock(w.End)
	if err != nil {
		return Errorf(CodeInvalidArgument, "horário de fim inválido %q", w.End)
	}
	w.Start, w.End = formatClock(s), formatClock(e)
	return nil
}

// String renders the window the way ParseWindow reads it back.
func (w Window) String() string {
	return DaysLabel(w.Days) + " " + w.Start + "-" + w.End
}

// parseClock accepts 9, 09, 9h, 9:30, 09h30 and 24:00.
func parseClock(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	h, m, found := strings.Cut(strings.ReplaceAll(s, "h", ":"), ":")
	if !found {
		h = s
	}
	hh, err := strconv.Atoi(h)
	if err != nil || hh < 0 || hh > 24 {
		return 0, fmt.Errorf("hora inválida %q", s)
	}
	mm := 0
	if m != "" {
		if mm, err = strconv.Atoi(m); err != nil || mm < 0 || mm > 59 || len(m) != 2 {
			return 0, fmt.Errorf("minuto inválido %q", s)
		}
	}
	if hh == 24 && mm != 0 {
		return 0, fmt.Errorf("hora inválida %q", s)
	}
	return hh*60 + mm, nil
}

func formatClock(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

var dayNames = map[string]int{
	"dom": 0, "domingo": 0, "sun": 0,
	"seg": 1, "segunda": 1, "mon": 1,
	"ter": 2, "terca": 2, "terça": 2, "tue": 2,
	"qua": 3, "quarta": 3, "wed": 3,
	"qui": 4, "quinta": 4, "thu": 4,
	"sex": 5, "sexta": 5, "fri": 5,
	"sab": 6, "sabado": 6, "sáb": 6, "sábado": 6, "sat": 6,
}

// DayShort are the labels used everywhere, indexed by time.Weekday.
var DayShort = [7]string{"dom", "seg", "ter", "qua", "qui", "sex", "sáb"}

// ParseDays reads "seg-sex", "sab,dom", "uteis", "fds", "todos" (and mixes).
// Ranges wrap around the week: "sex-seg" is sex, sáb, dom, seg.
func ParseDays(s string) ([]int, error) {
	var out []int
	for _, tok := range strings.Split(strings.ToLower(s), ",") {
		tok = strings.TrimSpace(strings.ReplaceAll(tok, "–", "-"))
		switch tok {
		case "":
			continue
		case "todos", "todo-dia", "diario", "diário", "all":
			out = append(out, 0, 1, 2, 3, 4, 5, 6)
			continue
		case "uteis", "úteis", "dias-uteis", "weekdays":
			out = append(out, 1, 2, 3, 4, 5)
			continue
		case "fds", "fim-de-semana", "weekend":
			out = append(out, 6, 0)
			continue
		}
		a, b, isRange := strings.Cut(tok, "-")
		from, ok := dayNames[strings.TrimSpace(a)]
		if !ok {
			return nil, Errorf(CodeInvalidArgument, "dia da semana desconhecido %q", a)
		}
		if !isRange {
			out = append(out, from)
			continue
		}
		to, ok := dayNames[strings.TrimSpace(b)]
		if !ok {
			return nil, Errorf(CodeInvalidArgument, "dia da semana desconhecido %q", b)
		}
		for d := from; ; d = (d + 1) % 7 {
			out = append(out, d)
			if d == to {
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, Errorf(CodeInvalidArgument, "nenhum dia da semana em %q", s)
	}
	return sortedInts(out), nil
}

// ParseWindow reads "seg-sex 09:00-18:00"; without days it means every day.
func ParseWindow(s string) (Window, error) {
	f := strings.Fields(strings.ReplaceAll(s, "–", "-"))
	if len(f) == 0 {
		return Window{}, Errorf(CodeInvalidArgument, "janela vazia")
	}
	days := "todos"
	if len(f) > 1 {
		days = strings.Join(f[:len(f)-1], ",")
	}
	start, end, ok := strings.Cut(f[len(f)-1], "-")
	if !ok {
		return Window{}, Errorf(CodeInvalidArgument, "janela %q: use dias e HH:MM-HH:MM, ex.: \"seg-sex 09:00-18:00\"", s)
	}
	d, err := ParseDays(days)
	if err != nil {
		return Window{}, err
	}
	w := Window{Days: d, Start: start, End: end}
	return w, w.Validate()
}

// DaysLabel compresses days into runs in Monday-first order: "seg-sex",
// "sáb-dom", "seg,qua,sex", "todos".
func DaysLabel(days []int) string {
	if len(days) == 7 {
		return "todos"
	}
	in := [7]bool{}
	for _, d := range days {
		if d >= 0 && d < 7 {
			in[d] = true
		}
	}
	order := []int{1, 2, 3, 4, 5, 6, 0}
	var parts []string
	for i := 0; i < len(order); {
		if !in[order[i]] {
			i++
			continue
		}
		j := i
		for j+1 < len(order) && in[order[j+1]] {
			j++
		}
		switch {
		case j == i:
			parts = append(parts, DayShort[order[i]])
		case j == i+1:
			parts = append(parts, DayShort[order[i]], DayShort[order[j]])
		default:
			parts = append(parts, DayShort[order[i]]+"-"+DayShort[order[j]])
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// DateLayout is how skipped dates are written.
const DateLayout = "2006-01-02"

// Schedule switches profiles on during weekly windows.
type Schedule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Profiles []string `json:"profiles"`
	Windows  []Window `json:"windows"`
	Enabled  bool     `json:"enabled"`
	Guard    Guard    `json:"guard"`
	// Skip lists dates whose occurrences (by start day) do not happen.
	Skip []string `json:"skip"`
}

func (s *Schedule) Validate() error {
	if !ValidID(s.ID) {
		return Errorf(CodeInvalidArgument, "id de agenda inválido %q", s.ID)
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return Errorf(CodeInvalidArgument, "a agenda precisa de um nome")
	}
	if len(s.Profiles) == 0 {
		return Errorf(CodeInvalidArgument, "a agenda %s precisa de pelo menos um perfil", s.Name)
	}
	if len(s.Windows) == 0 {
		return Errorf(CodeInvalidArgument, "a agenda %s precisa de pelo menos um horário", s.Name)
	}
	for i := range s.Windows {
		if err := s.Windows[i].Validate(); err != nil {
			return err
		}
	}
	for _, d := range s.Skip {
		if _, err := time.Parse(DateLayout, d); err != nil {
			return Errorf(CodeInvalidArgument, "data inválida %q (use AAAA-MM-DD)", d)
		}
	}
	s.Profiles = sortedSet(s.Profiles)
	s.Skip = sortedSet(s.Skip)
	return s.Guard.Normalize()
}

// Occurrence is one concrete run of a schedule or focus session.
type Occurrence struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (o Occurrence) Contains(t time.Time) bool { return !t.Before(o.Start) && t.Before(o.End) }

// horizon is how far ahead occurrences are looked up; windows that chain into
// each other (every day 00:00–00:00) are merged up to it.
const horizon = 8

// occurrences lists the merged occurrences starting on days around t, sorted.
// Back-to-back or overlapping windows merge into one, so "the end of the
// current block" is when blocking really stops.
func (s Schedule) occurrences(t time.Time) []Occurrence {
	var raw []Occurrence
	y, m, d := t.Date()
	for i := -1; i <= horizon; i++ {
		day := time.Date(y, m, d+i, 0, 0, 0, 0, t.Location())
		if slices.Contains(s.Skip, day.Format(DateLayout)) {
			continue
		}
		for _, w := range s.Windows {
			if !slices.Contains(w.Days, int(day.Weekday())) {
				continue
			}
			st, dur := w.span()
			dy, dm, dd := day.Date()
			raw = append(raw, Occurrence{
				Start: time.Date(dy, dm, dd, 0, st, 0, 0, t.Location()),
				End:   time.Date(dy, dm, dd, 0, st+dur, 0, 0, t.Location()),
			})
		}
	}
	slices.SortFunc(raw, func(a, b Occurrence) int { return a.Start.Compare(b.Start) })
	var out []Occurrence
	for _, o := range raw {
		if n := len(out); n > 0 && !o.Start.After(out[n-1].End) {
			if o.End.After(out[n-1].End) {
				out[n-1].End = o.End
			}
			continue
		}
		out = append(out, o)
	}
	return out
}

// Current returns the occurrence in effect at t, ignoring Enabled and pauses.
func (s Schedule) Current(t time.Time) (Occurrence, bool) {
	for _, o := range s.occurrences(t) {
		if o.Contains(t) {
			return o, true
		}
	}
	return Occurrence{}, false
}

// Next returns the first occurrence starting after t.
func (s Schedule) Next(t time.Time) (Occurrence, bool) {
	for _, o := range s.occurrences(t) {
		if o.Start.After(t) {
			return o, true
		}
	}
	return Occurrence{}, false
}

// FocusSession switches profiles on from Start until Until.
type FocusSession struct {
	ID       string    `json:"id"`
	Profiles []string  `json:"profiles"`
	Start    time.Time `json:"start"`
	Until    time.Time `json:"until"`
	Guard    Guard     `json:"guard"`
}

// Pause switches a source off between From and Until. With friction, From is
// in the future: the source keeps blocking during the wait.
type Pause struct {
	Source string    `json:"source"`
	From   time.Time `json:"from"`
	Until  time.Time `json:"until"`
}

func (p Pause) InEffect(t time.Time) bool { return !t.Before(p.From) && t.Before(p.Until) }

func ScheduleSource(id string) string { return "schedule:" + id }
func FocusSource(id string) string    { return "focus:" + id }

// Source is a schedule occurrence or focus session that is blocking now. Its
// ID doubles as the BlockedBy reason of the apps it holds.
type Source struct {
	ID       string
	Name     string
	Profiles []string
	Guard    Guard
	Until    time.Time
}

// PauseOf returns the pause registered for a source, in effect or pending.
func (st State) PauseOf(source string) (Pause, bool) {
	i := slices.IndexFunc(st.Pauses, func(p Pause) bool { return p.Source == source })
	if i < 0 {
		return Pause{}, false
	}
	return st.Pauses[i], true
}

func (st State) paused(source string, t time.Time) bool {
	p, ok := st.PauseOf(source)
	return ok && p.InEffect(t)
}

// ActiveSources lists what is switching profiles on at t: enabled schedules
// inside an occurrence and running focus sessions, minus paused ones.
func (st State) ActiveSources(t time.Time) []Source {
	var out []Source
	for _, s := range st.Schedules {
		if !s.Enabled {
			continue
		}
		occ, ok := s.Current(t)
		if !ok || st.paused(ScheduleSource(s.ID), t) {
			continue
		}
		out = append(out, Source{ID: ScheduleSource(s.ID), Name: s.Name, Profiles: s.Profiles, Guard: s.Guard, Until: occ.End})
	}
	for _, f := range st.Focus {
		if t.Before(f.Start) || !t.Before(f.Until) || st.paused(FocusSource(f.ID), t) {
			continue
		}
		out = append(out, Source{ID: FocusSource(f.ID), Name: "Foco", Profiles: f.Profiles, Guard: f.Guard, Until: f.Until})
	}
	return out
}

// Locks are the active sources that cannot simply be switched off.
func (st State) Locks(t time.Time) []Source {
	return slices.DeleteFunc(st.ActiveSources(t), func(s Source) bool { return s.Guard.Mode == GuardFree })
}

// NextChange is the earliest moment after t when what is blocked may change
// by itself, or nil when nothing is scheduled.
func (st State) NextChange(t time.Time) *time.Time {
	var best *time.Time
	consider := func(c time.Time) {
		if c.After(t) && (best == nil || c.Before(*best)) {
			best = &c
		}
	}
	for _, s := range st.Schedules {
		if !s.Enabled {
			continue
		}
		if o, ok := s.Current(t); ok {
			consider(o.End)
		}
		if o, ok := s.Next(t); ok {
			consider(o.Start)
		}
	}
	for _, f := range st.Focus {
		consider(f.Start)
		consider(f.Until)
	}
	for _, p := range st.Pauses {
		consider(p.From)
		consider(p.Until)
	}
	return best
}

// Prune drops what can no longer matter at t: ended focus sessions, expired
// pauses or pauses of sources that are gone, and skipped dates in the past.
// It reports whether anything changed.
func (st *State) Prune(t time.Time) bool {
	n := len(st.Focus) + len(st.Pauses)
	st.Focus = slices.DeleteFunc(st.Focus, func(f FocusSession) bool { return !t.Before(f.Until) })
	exists := map[string]bool{}
	for _, s := range st.Schedules {
		exists[ScheduleSource(s.ID)] = true
	}
	for _, f := range st.Focus {
		exists[FocusSource(f.ID)] = true
	}
	st.Pauses = slices.DeleteFunc(st.Pauses, func(p Pause) bool { return !t.Before(p.Until) || !exists[p.Source] })
	changed := n != len(st.Focus)+len(st.Pauses)
	// Yesterday stays: an occurrence that started then may still be running.
	y, m, d := t.Date()
	keepFrom := time.Date(y, m, d-1, 0, 0, 0, 0, t.Location()).Format(DateLayout)
	for i := range st.Schedules {
		before := len(st.Schedules[i].Skip)
		st.Schedules[i].Skip = slices.DeleteFunc(st.Schedules[i].Skip, func(s string) bool { return s < keepFrom })
		changed = changed || before != len(st.Schedules[i].Skip)
	}
	return changed
}

// When formats t relative to now: "15:04", "amanhã 15:04" or "02/01 15:04".
func When(t, now time.Time) string {
	t = t.In(now.Location())
	y, m, d := now.Date()
	ty, tm, td := t.Date()
	switch {
	case ty == y && tm == m && td == d:
		return t.Format("15:04")
	case time.Date(y, m, d+1, 0, 0, 0, 0, now.Location()).Equal(time.Date(ty, tm, td, 0, 0, 0, 0, now.Location())):
		return "amanhã " + t.Format("15:04")
	}
	return t.Format("02/01 15:04")
}

func sortedInts(in []int) []int {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// sortFocus keeps sessions ordered by end time.
func sortFocus(fs []FocusSession) {
	slices.SortFunc(fs, func(a, b FocusSession) int { return cmp.Or(a.Until.Compare(b.Until), strings.Compare(a.ID, b.ID)) })
}
