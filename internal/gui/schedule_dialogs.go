package gui

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Monday-first, like the grid.
var weekOrder = []int{1, 2, 3, 4, 5, 6, 0}

type windowRow struct {
	days       [7]bool
	start, end string
}

type scheduleEditor struct {
	id         string // empty when creating
	name       string
	profiles   map[string]bool
	rows       []windowRow
	mode       domain.GuardMode
	delay      string
	phrase     string
	skipInput  string
	lockedNote string
	opened     bool
}

func newScheduleEditor(s snapshot, v *api.ScheduleView) *scheduleEditor {
	e := &scheduleEditor{profiles: map[string]bool{}, mode: domain.GuardFree, delay: strconv.Itoa(domain.DefaultDelayMinutes)}
	if v == nil {
		e.rows = []windowRow{presetRow([]int{1, 2, 3, 4, 5}, "09:00", "18:00")}
		return e
	}
	e.id, e.name, e.mode, e.phrase = v.ID, v.Name, v.Guard.Mode, v.Guard.Phrase
	if v.Guard.DelayMinutes > 0 {
		e.delay = strconv.Itoa(v.Guard.DelayMinutes)
	}
	for _, p := range v.Profiles {
		e.profiles[p] = true
	}
	for _, w := range v.Windows {
		e.rows = append(e.rows, presetRow(w.Days, w.Start, w.End))
	}
	if guarded(*v) {
		e.lockedNote = fmt.Sprintf("Valendo agora em modo %s até %s: mudanças que afrouxem o bloqueio serão recusadas até lá.",
			strings.ToLower(modeName(v.Guard.Mode)), domain.When(v.Current.End, time.Now()))
	}
	return e
}

func presetRow(days []int, start, end string) windowRow {
	r := windowRow{start: start, end: end}
	for _, d := range days {
		r.days[d] = true
	}
	return r
}

// spec validates the form; the message explains the first problem found.
func (e *scheduleEditor) spec() (ScheduleSpec, string) {
	sp := ScheduleSpec{Name: strings.TrimSpace(e.name)}
	if sp.Name == "" {
		return sp, "Dê um nome à agenda."
	}
	for id, on := range e.profiles {
		if on {
			sp.Profiles = append(sp.Profiles, id)
		}
	}
	slices.Sort(sp.Profiles)
	if len(sp.Profiles) == 0 {
		return sp, "Escolha pelo menos um perfil."
	}
	if len(e.rows) == 0 {
		return sp, "Adicione pelo menos um horário."
	}
	for i, r := range e.rows {
		w := domain.Window{Start: r.start, End: r.end}
		for d, on := range r.days {
			if on {
				w.Days = append(w.Days, d)
			}
		}
		if err := w.Validate(); err != nil {
			return sp, fmt.Sprintf("Horário %d: %s.", i+1, domain.AsError(err).Message)
		}
		sp.Windows = append(sp.Windows, w)
	}
	g, msg := guardFrom(e.mode, e.delay, e.phrase)
	sp.Guard = g
	return sp, msg
}

func guardFrom(mode domain.GuardMode, delay, phrase string) (domain.Guard, string) {
	g := domain.Guard{Mode: mode, Phrase: phrase}
	if mode == domain.GuardFriction {
		n, err := strconv.Atoi(strings.TrimSpace(delay))
		if err != nil || n < 1 {
			return g, "A espera da fricção é um número de minutos."
		}
		g.DelayMinutes = n
	}
	if err := g.Normalize(); err != nil {
		return g, domain.AsError(err).Message
	}
	return g, ""
}

type focusForm struct {
	profiles map[string]bool
	minutes  int
	custom   string
	mode     domain.GuardMode
	delay    string
	phrase   string
	opened   bool
}

func newFocusForm() *focusForm {
	return &focusForm{profiles: map[string]bool{}, minutes: 50, mode: domain.GuardFree, delay: strconv.Itoa(domain.DefaultDelayMinutes)}
}

// pauseDialog asks for the friction phrase before pausing a schedule or
// ending a focus session.
type pauseDialog struct {
	focus  bool
	id     string
	name   string
	guard  domain.Guard
	until  time.Time
	input  string
	opened bool
}

func (a *App) scheduleDialogs(s snapshot) {
	if e := a.schedEd; e != nil {
		modal("schedule-editor", &e.opened, 820, 720, func() bool {
			keep := a.scheduleEditorBody(s, e)
			if !keep {
				a.schedEd = nil
			}
			return keep
		})
	}
	if f := a.focusForm; f != nil {
		modal("focus-form", &f.opened, 620, 500, func() bool {
			keep := a.focusFormBody(s, f)
			if !keep {
				a.focusForm = nil
			}
			return keep
		})
	}
	if p := a.pauseDlg; p != nil {
		modal("pause-dialog", &p.opened, 540, 340, func() bool {
			keep := a.pauseDialogBody(p)
			if !keep {
				a.pauseDlg = nil
			}
			return keep
		})
	}
}

// ── shared widgets ─────────────────────────────────────────────────────

// pillToggle is a pill that flips on click; returns true when clicked.
func (a *App) pillToggle(id, label string, on bool, accent color.RGBA) bool {
	fg, bg := colMuted, colSurface3
	if on {
		fg, bg = colText, mix(colSurface3, accent, 0.55)
	}
	imgui.PushIDStr(id)
	a.pill(label, fg, bg)
	imgui.PopID()
	clicked := imgui.IsItemClicked()
	handCursorIfHovered()
	return clicked
}

// flow lays out items left to right, wrapping at the content width.
type flow struct {
	startX, maxX float32
	first        bool
}

func newFlow() *flow {
	return &flow{startX: imgui.CursorPosX(), maxX: imgui.CursorPosX() + imgui.ContentRegionAvail().X, first: true}
}

func (f *flow) next(w, gap float32) {
	if !f.first {
		imgui.SameLineV(0, gap)
		if imgui.CursorPosX()+w > f.maxX {
			imgui.NewLine()
			imgui.SetCursorPosX(f.startX)
		}
	}
	f.first = false
}

func (a *App) fieldLabel(l string) { a.medium(13, func() { text(l, colMuted) }) }

func (a *App) profilePicker(s snapshot, prefix string, sel map[string]bool) {
	fl := newFlow()
	for _, p := range s.profiles {
		fl.next(a.pillWidth(p.Name), 6)
		if a.pillToggle(prefix+p.ID, p.Name, sel[p.ID], colAccent) {
			sel[p.ID] = !sel[p.ID]
		}
	}
}

var guardChoices = []struct {
	mode                     domain.GuardMode
	title, schedDesc, fcDesc string
}{
	{domain.GuardFree, "Livre", "Pausa na hora, até o fim do horário.", "Encerra quando quiser."},
	{domain.GuardFriction, "Fricção", "Digitar uma frase e esperar alguns minutos.", "Digitar uma frase e esperar alguns minutos."},
	{domain.GuardLocked, "Travado", "Sem saída até o horário acabar.", "Sem saída até o tempo acabar."},
}

// guardPicker is a three-way segmented control plus the friction fields.
func (a *App) guardPicker(id string, focus bool, mode *domain.GuardMode, delay, phrase *string) {
	w := imgui.ContentRegionAvail().X
	const gap, h = 10, 74
	bw := (w - 2*gap) / 3
	dl := imgui.WindowDrawList()
	for i, c := range guardChoices {
		if i > 0 {
			imgui.SameLineV(0, gap)
		}
		pos := imgui.CursorScreenPos()
		if imgui.InvisibleButton(id+"/"+string(c.mode), vec(bw, h)) {
			*mode = c.mode
		}
		hovered := imgui.IsItemHovered()
		handCursorIfHovered()
		on := *mode == c.mode
		col := modeColor(c.mode)
		if c.mode == domain.GuardFree {
			col = colAccent
		}
		bg := colSurface2
		if on {
			bg = mix(colSurface2, col, 0.14)
		} else if hovered {
			bg = colSurface3
		}
		dl.AddRectFilledV(pos, vec(pos.X+bw, pos.Y+h), u32(bg), 10, imgui.DrawFlagsRoundCornersAll)
		if on {
			dl.AddRectV(pos, vec(pos.X+bw, pos.Y+h), u32(alpha(col, 200)), 10, imgui.DrawFlagsRoundCornersAll, 1.4)
		}
		tx := pos.X + 14
		if c.mode == domain.GuardLocked {
			lockIcon(dl, vec(pos.X+20, pos.Y+19), col)
			tx += 16
		}
		a.medium(14, func() { dl.AddTextVec2(vec(tx, pos.Y+10), u32(colText), c.title) })
		a.font(nil, 12, func() {
			clip := imgui.Vec4{X: pos.X, Y: pos.Y, Z: pos.X + bw, W: pos.Y + h}
			desc := c.schedDesc
			if focus {
				desc = c.fcDesc
			}
			dl.AddTextFontPtrV(imgui.CurrentFont(), imgui.FontSize(), vec(pos.X+14, pos.Y+34), u32(colMuted), desc, bw-28, &clip)
		})
	}
	if *mode == domain.GuardFriction {
		imgui.Dummy(vec(0, 2))
		startX := imgui.CursorPosX()
		a.fieldLabel("Espera (min)")
		imgui.SameLineV(startX+120, 0)
		a.fieldLabel("Frase para pausar (vazia = padrão)")
		input(id+"-delay", delay, "10", 104)
		imgui.SameLineV(0, 16)
		input(id+"-phrase", phrase, domain.DefaultPhrase, imgui.ContentRegionAvail().X)
	}
}

// footer draws the error (if any) and Cancel/confirm on the modal's last
// line; it returns cancel and confirm clicks.
func (a *App) footer(id, errMsg, confirm string, kind btnKind) (cancel, ok bool) {
	imgui.SetCursorPosY(imgui.WindowSize().Y - 26 - 36)
	right := imgui.WindowWidth() - 30
	cw, lw := a.buttonWidth("Cancelar"), a.buttonWidth(confirm)
	if errMsg != "" {
		y := imgui.CursorPosY()
		imgui.SetCursorPosY(y + 9)
		a.font(nil, 13, func() { text(ellipsize(errMsg, right-cw-lw-60), colWarn) })
		imgui.SetCursorPosY(y)
	}
	imgui.SetCursorPosX(right - cw - lw - 8)
	cancel = a.button("Cancelar##"+id, btnSecondary, 0)
	imgui.SameLineV(0, 8)
	imgui.BeginDisabledV(errMsg != "")
	ok = a.button(confirm+"##"+id, kind, 0)
	imgui.EndDisabled()
	return cancel, ok
}

// ── schedule editor ────────────────────────────────────────────────────

var windowPresets = []struct {
	label      string
	days       []int
	start, end string
}{
	{"Dias úteis 09–18", []int{1, 2, 3, 4, 5}, "09:00", "18:00"},
	{"Noites 23–07", []int{0, 1, 2, 3, 4, 5, 6}, "23:00", "07:00"},
	{"Fim de semana", []int{6, 0}, "00:00", "00:00"},
	{"Manhãs 06–12", []int{1, 2, 3, 4, 5}, "06:00", "12:00"},
}

func (a *App) scheduleEditorBody(s snapshot, e *scheduleEditor) bool {
	title := "Nova agenda"
	if e.id != "" {
		title = "Editar agenda"
	}
	a.bold(22, func() { text(title, colText) })
	a.font(nil, 13.5, func() { text("Os perfis escolhidos ligam sozinhos nos horários abaixo.", colMuted) })
	imgui.Dummy(vec(0, 2))

	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(2, 2))
	if imgui.BeginChildStrV("se-body", vec(0, -64), imgui.ChildFlagsAlwaysUseWindowPadding, 0) {
		w := imgui.ContentRegionAvail().X - 8
		if e.lockedNote != "" {
			card("se-locked", w, 0, alpha(colWarn, 26), func() { wrapped(e.lockedNote, colText, w-40) })
		}
		a.fieldLabel("Nome")
		input("se-name", &e.name, "ex.: Trabalho", w)

		imgui.Dummy(vec(0, 2))
		a.fieldLabel("Perfis que ela liga")
		a.profilePicker(s, "se-p/", e.profiles)

		imgui.Dummy(vec(0, 4))
		a.fieldLabel("Horários")
		imgui.SameLineV(0, 12)
		a.font(nil, 12, func() { text("atalhos:", colFaint) })
		for _, p := range windowPresets {
			imgui.SameLineV(0, 6)
			a.pill(p.label, colAccentHi, alpha(colAccent, 30))
			handCursorIfHovered()
			if imgui.IsItemClicked() {
				e.rows = append(e.rows, presetRow(p.days, p.start, p.end))
			}
		}
		remove := -1
		for i := range e.rows {
			if a.windowRowEditor(fmt.Sprintf("se-row%d", i), &e.rows[i], len(e.rows) > 1) {
				remove = i
			}
		}
		if remove >= 0 {
			e.rows = slices.Delete(e.rows, remove, remove+1)
		}
		a.font(nil, 13, func() {
			text("+ Adicionar horário", colAccentHi)
			handCursorIfHovered()
			if imgui.IsItemClicked() {
				e.rows = append(e.rows, presetRow(nil, "09:00", "12:00"))
			}
		})

		imgui.Dummy(vec(0, 4))
		a.fieldLabel("Enquanto estiver valendo, desligar é…")
		a.guardPicker("se-guard", false, &e.mode, &e.delay, &e.phrase)

		if e.id != "" {
			imgui.Dummy(vec(0, 4))
			a.fieldLabel("Datas puladas")
			// Read live from the daemon: skips apply right away, not on save.
			var skip []string
			if i := slices.IndexFunc(s.schedules, func(v api.ScheduleView) bool { return v.ID == e.id }); i >= 0 {
				skip = s.schedules[i].Skip
			}
			fl := newFlow()
			for _, d := range skip {
				t, _ := time.Parse(domain.DateLayout, d)
				label := t.Format("02/01") + "  ×"
				fl.next(a.pillWidth(label), 6)
				a.pill(label, colText, colSurface3)
				handCursorIfHovered()
				tooltip("Clique para voltar a valer nessa data.")
				if imgui.IsItemClicked() {
					id, date := e.id, d
					a.do("", false, "A agenda volta a valer em "+t.Format("02/01")+".", func(ctx ctxT) error {
						return a.ctl.ScheduleSkip(ctx, id, date, true)
					})
				}
			}
			if len(skip) == 0 {
				a.font(nil, 12.5, func() { text("Nenhuma. Pule feriados e folgas sem desligar a agenda.", colFaint) })
			}
			input("se-skip", &e.skipInput, "DD/MM, AAAA-MM-DD, hoje ou amanha", 300)
			imgui.SameLineV(0, 8)
			raw := strings.TrimSpace(e.skipInput)
			imgui.BeginDisabledV(raw == "")
			if a.button("Pular data", btnSecondary, 0) {
				id := e.id
				e.skipInput = ""
				a.do("", false, "Data pulada.", func(ctx ctxT) error { return a.ctl.ScheduleSkip(ctx, id, raw, false) })
			}
			imgui.EndDisabled()
		}
	}
	imgui.EndChild()
	imgui.PopStyleVarV(1)

	spec, msg := e.spec()
	save := "Criar agenda"
	if e.id != "" {
		save = "Salvar"
	}
	cancel, ok := a.footer("se", msg, save, btnPrimary)
	if ok {
		id := e.id
		done := "Agenda " + spec.Name + " criada."
		if id != "" {
			done = "Agenda " + spec.Name + " salva."
		}
		a.do("", false, done, func(ctx ctxT) error { return a.ctl.ScheduleSave(ctx, id, spec) })
	}
	return !cancel && !ok
}

// windowRowEditor edits one window; returns true when its remove button is
// clicked.
func (a *App) windowRowEditor(id string, r *windowRow, removable bool) bool {
	imgui.Dummy(vec(0, 0))
	fl := newFlow()
	for _, d := range weekOrder {
		label := domain.DayShort[d]
		fl.next(a.pillWidth(label), 4)
		if a.pillToggle(fmt.Sprintf("%s/d%d", id, d), label, r.days[d], colAccent) {
			r.days[d] = !r.days[d]
		}
	}
	imgui.SameLineV(0, 16)
	y := imgui.CursorPosY()
	imgui.SetCursorPosY(y - 6)
	input(id+"/start", &r.start, "09:00", 80)
	imgui.SameLineV(0, 8)
	imgui.SetCursorPosY(y)
	a.font(nil, 13, func() { text("até", colMuted) })
	imgui.SameLineV(0, 8)
	imgui.SetCursorPosY(y - 6)
	input(id+"/end", &r.end, "18:00", 80)
	if s, err1 := strconv.Atoi(strings.ReplaceAll(r.start, ":", "")); err1 == nil {
		if e, err2 := strconv.Atoi(strings.ReplaceAll(r.end, ":", "")); err2 == nil && e <= s {
			imgui.SameLineV(0, 10)
			imgui.SetCursorPosY(y)
			a.font(nil, 12, func() { text("até o dia seguinte", colFaint) })
		}
	}
	removed := false
	if removable {
		imgui.SameLineV(0, 8)
		imgui.SetCursorPosY(y - 6)
		removed = a.button("×##"+id, btnGhost, 0)
		tooltip("Remover este horário")
	}
	return removed
}

// ── focus form ─────────────────────────────────────────────────────────

var focusPresets = []int{25, 50, 60, 90, 120, 240}

func (a *App) focusFormBody(s snapshot, f *focusForm) bool {
	a.bold(22, func() { text("Foco agora", colText) })
	a.font(nil, 13.5, func() { text("Liga perfis agora e desliga sozinho no fim.", colMuted) })
	imgui.Dummy(vec(0, 4))

	a.fieldLabel("Perfis")
	a.profilePicker(s, "ff-p/", f.profiles)
	imgui.Dummy(vec(0, 6))

	a.fieldLabel("Por quanto tempo")
	fl := newFlow()
	custom, _ := strconv.Atoi(strings.TrimSpace(f.custom))
	for _, m := range focusPresets {
		label := remaining(time.Duration(m) * time.Minute)
		fl.next(a.pillWidth(label), 6)
		if a.pillToggle(fmt.Sprintf("ff-m%d", m), label, f.minutes == m && custom <= 0, colBlock) {
			f.minutes, f.custom = m, ""
		}
	}
	imgui.SameLineV(0, 12)
	y := imgui.CursorPosY()
	imgui.SetCursorPosY(y - 6)
	input("ff-custom", &f.custom, "outro (min)", 120)
	minutes := f.minutes
	if custom > 0 {
		minutes = custom
	}
	a.font(nil, 12.5, func() {
		text("Termina às "+time.Now().Add(time.Duration(minutes)*time.Minute).Format("15:04")+".", colFaint)
	})
	imgui.Dummy(vec(0, 6))

	a.fieldLabel("Enquanto durar, encerrar é…")
	a.guardPicker("ff-guard", true, &f.mode, &f.delay, &f.phrase)

	var profiles []string
	for id, on := range f.profiles {
		if on {
			profiles = append(profiles, id)
		}
	}
	slices.Sort(profiles)
	g, msg := guardFrom(f.mode, f.delay, f.phrase)
	if len(profiles) == 0 {
		msg = "Escolha pelo menos um perfil."
	}
	cancel, ok := a.footer("ff", msg, "Começar foco", btnDanger)
	if ok {
		a.do("", false, "Foco até "+time.Now().Add(time.Duration(minutes)*time.Minute).Format("15:04")+".", func(ctx ctxT) error {
			return a.ctl.FocusStart(ctx, profiles, minutes, g)
		})
	}
	return !cancel && !ok
}

// ── friction pause ─────────────────────────────────────────────────────

func (a *App) pauseDialogBody(p *pauseDialog) bool {
	now := time.Now()
	from := now.Add(time.Duration(p.guard.DelayMinutes) * time.Minute)
	title, verb := "Pausar "+p.name+"?", "Pedir pausa"
	body := fmt.Sprintf("A pausa começa %d min depois do pedido (por volta das %s) e vale até %s. Dá para cancelar o pedido antes disso.",
		p.guard.DelayMinutes, from.Format("15:04"), domain.When(p.until, now))
	if p.focus {
		title, verb = "Encerrar o foco?", "Pedir para encerrar"
		body = fmt.Sprintf("O foco acaba %d min depois do pedido (por volta das %s). Dá para cancelar o pedido antes disso.",
			p.guard.DelayMinutes, from.Format("15:04"))
	}
	a.bold(20, func() { text(title, colText) })
	w := imgui.ContentRegionAvail().X
	wrapped(body, colMuted, w)
	imgui.Dummy(vec(0, 4))
	a.fieldLabel("Digite exatamente:")
	card("pd-phrase", w, 0, colSurface2, func() { a.medium(15, func() { wrapped(p.guard.Phrase, colWarn, w-40) }) })
	input("pd-input", &p.input, "frase", w)
	msg := ""
	if !p.guard.PhraseMatches(p.input) {
		msg = "A frase ainda não confere."
		if strings.TrimSpace(p.input) == "" {
			msg = " " // keeps the button disabled without nagging
		}
	}
	cancel, ok := a.footer("pd", msg, verb, btnDanger)
	if ok {
		id, phrase := p.id, p.input
		done := fmt.Sprintf("Pausa pedida: começa às %s.", from.Format("15:04"))
		if p.focus {
			a.do("", false, done, func(ctx ctxT) error { return a.ctl.FocusStop(ctx, id, phrase) })
		} else {
			a.do("", false, done, func(ctx ctxT) error { return a.ctl.SchedulePause(ctx, id, phrase) })
		}
	}
	return !cancel && !ok
}
