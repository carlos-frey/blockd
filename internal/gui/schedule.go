package gui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Each schedule keeps its color (by position) in the grid, cards and dots.
var scheduleColors = []color.RGBA{
	rgb(0x8b7cff), rgb(0x5cc8ff), rgb(0x3dd68c), rgb(0xffb447), rgb(0xff6b9a), rgb(0x8bd450), rgb(0xf2c94c),
}

func scheduleColor(i int) color.RGBA { return scheduleColors[i%len(scheduleColors)] }

func profileName(s snapshot, id string) string {
	for _, p := range s.profiles {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

func scheduleName(s snapshot, id string) string {
	for _, v := range s.schedules {
		if v.ID == id {
			return v.Name
		}
	}
	return id
}

// clockMinutes reads the "HH:MM" the daemon normalizes windows to.
func clockMinutes(hm string) int {
	var h, m int
	fmt.Sscanf(hm, "%d:%d", &h, &m)
	return h*60 + m
}

// windowLabel is the friendly form of a window: "seg-sex  09:00–18:00".
func windowLabel(w domain.Window) string {
	return strings.ReplaceAll(domain.DaysLabel(w.Days), ",", ", ") + "  " + w.Start + "–" + w.End
}

func modeName(m domain.GuardMode) string {
	switch m {
	case domain.GuardFriction:
		return "Fricção"
	case domain.GuardLocked:
		return "Travado"
	}
	return "Livre"
}

func modeColor(m domain.GuardMode) color.RGBA {
	switch m {
	case domain.GuardFriction:
		return colWarn
	case domain.GuardLocked:
		return colBlock
	}
	return colMuted
}

// remaining renders how long is left, rounding minutes up.
func remaining(d time.Duration) string {
	m := int((d + time.Minute - 1) / time.Minute)
	if m < 60 {
		return fmt.Sprintf("%d min", max(m, 0))
	}
	if m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}

func (a *App) schedulePage(s snapshot, pending map[string]bool) {
	const newLabel = "+  Nova agenda"
	startY := imgui.CursorPosY()
	if a.header("Agenda", "Perfis que ligam sozinhos nos horários da semana.", newLabel) {
		a.schedEd = newScheduleEditor(s, nil)
	}
	endY := imgui.CursorPosY()
	imgui.SetCursorPos(vec(imgui.WindowWidth()-40-a.buttonWidth(newLabel)-10-a.buttonWidth("Foco agora"), startY+8))
	if a.button("Foco agora", btnSecondary, 0) {
		a.focusForm = newFocusForm()
	}
	imgui.SetCursorPosY(endY)

	avail := imgui.ContentRegionAvail().X
	now := time.Now()
	if n := len(s.status.Focus); n > 0 {
		a.section("Foco", fmt.Sprintf("— %d", n))
		for _, f := range s.status.Focus {
			a.focusCard(s, f, avail, now)
		}
	}

	a.section("Semana", "— "+weekRange(now))
	a.weekGrid(s, avail, now)

	a.section("Agendas", fmt.Sprintf("— %d", len(s.schedules)))
	if len(s.schedules) == 0 {
		card("no-sched", avail, 0, colSurface, func() {
			a.medium(15, func() { text("Nenhuma agenda ainda.", colText) })
			wrapped("Ex.: Trabalho, seg–sex 09:00–18:00 com Redes sociais e Vídeo. Ou Noite, todos os dias 23:00–07:00 com Games, travado.",
				colMuted, avail-40)
			imgui.Dummy(vec(0, 4))
			if a.button("Criar a primeira agenda", btnPrimary, 0) {
				a.schedEd = newScheduleEditor(s, nil)
			}
		})
		return
	}
	const gap = 16
	cols := 1
	switch {
	case avail > 1150:
		cols = 3
	case avail > 760:
		cols = 2
	}
	cw := (avail - float32(cols-1)*gap) / float32(cols)
	for i, v := range s.schedules {
		if i%cols != 0 {
			imgui.SameLineV(0, gap)
		}
		a.scheduleCard(s, v, scheduleColor(i), cw, now, pending)
	}
}

func weekRange(now time.Time) string {
	mon := weekStart(now)
	return mon.Format("02/01") + " a " + mon.AddDate(0, 0, 6).Format("02/01")
}

// weekStart is the Monday 00:00 of now's week.
func weekStart(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d-(int(now.Weekday())+6)%7, 0, 0, 0, 0, now.Location())
}

// ── focus ──────────────────────────────────────────────────────────────

func (a *App) focusCard(s snapshot, f api.FocusView, w float32, now time.Time) {
	const h = 92
	pos := imgui.CursorScreenPos()
	card("focus/"+f.ID, w, h, mix(colSurface, colBlock, 0.07), func() {
		dl := imgui.WindowDrawList()
		inner := imgui.ContentRegionAvail().X
		top := imgui.CursorScreenPos()

		a.bold(26, func() { text(remaining(f.Until.Sub(now)), colText) })
		imgui.SetCursorScreenPos(vec(top.X, top.Y+36))
		a.font(nil, 12.5, func() {
			text("até "+domain.When(f.Until, now)+"  ·  "+modeName(f.Guard.Mode), colMuted)
		})

		// Profiles in the middle.
		imgui.SetCursorScreenPos(vec(top.X+150, top.Y+8))
		for i, id := range f.Profiles {
			if i > 0 {
				imgui.SameLineV(0, 6)
			}
			a.pill(profileName(s, id), colText, colSurface3)
		}

		// Actions, right to left.
		x := top.X + inner
		place := func(width float32) {
			x -= width
			imgui.SetCursorScreenPos(vec(x, top.Y+10))
			x -= 8
		}
		id := f.ID
		switch {
		case f.Pause != nil:
			place(a.buttonWidth("Cancelar pedido"))
			if a.button("Cancelar pedido##"+id, btnGhost, 0) {
				a.do("", false, "Pedido cancelado: o foco continua.", func(ctx ctxT) error { return a.ctl.FocusResume(ctx, id) })
			}
			label := "encerra às " + domain.When(f.Pause.From, now)
			x -= a.pillWidth(label)
			imgui.SetCursorScreenPos(vec(x, top.Y+17))
			a.pill(label, colWarn, alpha(colWarn, 30))
			x -= 8
		case f.Guard.Mode == domain.GuardFree:
			place(a.buttonWidth("Encerrar"))
			if a.button("Encerrar##"+id, btnDanger, 0) {
				a.do("", false, "Foco encerrado.", func(ctx ctxT) error { return a.ctl.FocusStop(ctx, id, "") })
			}
		case f.Guard.Mode == domain.GuardFriction:
			place(a.buttonWidth("Pedir para encerrar"))
			if a.button("Pedir para encerrar##"+id, btnSecondary, 0) {
				a.pauseDlg = &pauseDialog{focus: true, id: id, name: "o foco", guard: f.Guard, until: f.Until}
			}
		default:
			label := "travado até " + domain.When(f.Until, now)
			a.medium(13, func() {
				tw := imgui.CalcTextSize(label).X
				x -= tw
				dl.AddTextVec2(vec(x, top.Y+18), u32(colBlock), label)
			})
			lockIcon(dl, vec(x-12, top.Y+26), colBlock)
			x -= 28
		}
		place(a.buttonWidth("+15 min"))
		if a.button("+15 min##"+id, btnSecondary, 0) {
			a.do("", false, "Foco estendido em 15 min.", func(ctx ctxT) error { return a.ctl.FocusExtend(ctx, id, 15) })
		}

		// Progress along the bottom edge.
		total := f.Until.Sub(f.Start).Seconds()
		done := float32(1)
		if total > 0 {
			done = float32(min(max(now.Sub(f.Start).Seconds()/total, 0), 1))
		}
		by := pos.Y + h - 4
		dl.AddRectFilledV(vec(pos.X+14, by), vec(pos.X+w-14, by+2), u32(alpha(colBlock, 40)), 1, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectFilledV(vec(pos.X+14, by), vec(pos.X+14+(w-28)*done, by+2), u32(colBlock), 1, imgui.DrawFlagsRoundCornersAll)
	})
	imgui.Dummy(vec(0, 2))
}

// ── week grid ──────────────────────────────────────────────────────────

func (a *App) weekGrid(s snapshot, w float32, now time.Time) {
	const hourH, headH, gutter = 11.0, 30.0, 38.0
	h := float32(headH + 24*hourH + 62)
	card("week", w, h, colSurface, func() {
		dl := imgui.WindowDrawList()
		inner := imgui.ContentRegionAvail().X
		top := imgui.CursorScreenPos()
		colW := (inner - gutter) / 7
		y0 := top.Y + headH
		gridBottom := y0 + 24*hourH
		mon := weekStart(now)
		today := (int(now.Weekday()) + 6) % 7
		colX := func(c int) float32 { return top.X + gutter + float32(c)*colW }

		// Day headers and column backgrounds.
		for c := 0; c < 7; c++ {
			day := mon.AddDate(0, 0, c)
			label := domain.DayShort[day.Weekday()] + " " + day.Format("02")
			bg := colSurface2
			fg := colMuted
			if c == today {
				bg, fg = mix(colSurface2, colAccent, 0.18), colText
			}
			dl.AddRectFilledV(vec(colX(c)+2, y0), vec(colX(c)+colW-2, gridBottom), u32(alpha(bg, 150)), 6, imgui.DrawFlagsRoundCornersAll)
			a.medium(12.5, func() {
				ts := imgui.CalcTextSize(label)
				dl.AddTextVec2(vec(colX(c)+(colW-ts.X)/2, top.Y+4), u32(fg), label)
			})
		}
		// Hour lines.
		a.font(nil, 11, func() {
			for hr := 0; hr <= 24; hr += 3 {
				y := y0 + float32(hr)*hourH
				dl.AddLineV(vec(top.X+gutter, y), vec(top.X+inner, y), u32(alpha(colBorder, 160)), 1)
				if hr < 24 {
					dl.AddTextVec2(vec(top.X, y-6), u32(colFaint), fmt.Sprintf("%02dh", hr))
				}
			}
		})

		// Occurrences split at midnight and clipped to this week. Like a
		// calendar app, a block only shares the column width with the blocks
		// it overlaps in time.
		type segment struct {
			k, col, from, to int // schedule index, column, minutes in the day
			tip              string
			enabled, skipped bool
			lane, lanes      int
		}
		var segs []segment
		for k, v := range s.schedules {
			for i := -1; i < 7; i++ {
				day := mon.AddDate(0, 0, i)
				skipped := slices.Contains(v.Skip, day.Format(domain.DateLayout))
				for _, win := range v.Windows {
					if !slices.Contains(win.Days, int(day.Weekday())) {
						continue
					}
					st, end := clockMinutes(win.Start), clockMinutes(win.End)
					if end <= st {
						end += 24 * 60
					}
					from, to := i*24*60+st, i*24*60+end
					tip := v.Name + "  ·  " + domain.DayShort[day.Weekday()] + " " + win.Start + "–" + win.End
					switch {
					case skipped:
						tip += "  (pulada)"
					case !v.Enabled:
						tip += "  (desligada)"
					}
					for c := max(from/(24*60), 0); c < 7 && c*24*60 < to; c++ {
						s0, s1 := max(from, c*24*60)-c*24*60, min(to, (c+1)*24*60)-c*24*60
						if s1 <= s0 {
							continue
						}
						segs = append(segs, segment{k: k, col: c, from: s0, to: s1, tip: tip, enabled: v.Enabled, skipped: skipped})
					}
				}
			}
		}
		layoutLanes(segs, func(sg *segment) (int, int, int) { return sg.col, sg.from, sg.to },
			func(sg *segment, lane, lanes int) { sg.lane, sg.lanes = lane, lanes })
		tip := ""
		hoverable := imgui.IsWindowHovered() // false under a modal
		for _, sg := range segs {
			laneW := (colW - 8) / float32(sg.lanes)
			p0 := vec(colX(sg.col)+4+float32(sg.lane)*laneW, y0+float32(sg.from)/60*hourH)
			p1 := vec(p0.X+laneW-2, y0+float32(sg.to)/60*hourH)
			a.occurrence(dl, p0, p1, scheduleColor(sg.k), sg.enabled, sg.skipped)
			if hoverable && imgui.IsMouseHoveringRect(p0, p1) {
				tip = sg.tip
			}
		}

		// Now line.
		ny := y0 + float32(now.Hour()*60+now.Minute())/60*hourH
		dl.AddLineV(vec(colX(today)+2, ny), vec(colX(today)+colW-2, ny), u32(colBlock), 2)
		dl.AddCircleFilledV(vec(colX(today)+3, ny), 4, u32(colBlock), 12)

		// Legend.
		x := top.X + gutter
		ly := gridBottom + 12
		a.font(nil, 12, func() {
			for k, v := range s.schedules {
				dl.AddRectFilledV(vec(x, ly+3), vec(x+10, ly+13), u32(scheduleColor(k)), 3, imgui.DrawFlagsRoundCornersAll)
				fg := colMuted
				if !v.Enabled {
					fg = colFaint
				}
				dl.AddTextVec2(vec(x+16, ly), u32(fg), v.Name)
				x += 16 + imgui.CalcTextSize(v.Name).X + 18
			}
			if len(s.schedules) == 0 {
				dl.AddTextVec2(vec(x, ly), u32(colFaint), "As agendas aparecem aqui.")
			}
		})
		imgui.Dummy(vec(inner, h-32))
		if tip != "" {
			imgui.SetTooltip(strings.ReplaceAll(tip, "%", "%%"))
		}
	})
}

// layoutLanes assigns lanes inside each group of time-overlapping items of
// the same column: an item alone gets the full width (lanes = 1).
func layoutLanes[T any](items []T, key func(*T) (col, from, to int), set func(*T, int, int)) {
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(x, y int) int {
		cx, fx, _ := key(&items[x])
		cy, fy, _ := key(&items[y])
		if cx != cy {
			return cx - cy
		}
		return fx - fy
	})
	for start := 0; start < len(idx); {
		// Grow the group while the next item starts before the group ends.
		col, _, end := key(&items[idx[start]])
		stop := start + 1
		for stop < len(idx) {
			c, f, t := key(&items[idx[stop]])
			if c != col || f >= end {
				break
			}
			end = max(end, t)
			stop++
		}
		var laneEnd []int // when each lane frees up
		lanes := make([]int, stop-start)
		for j := start; j < stop; j++ {
			_, f, t := key(&items[idx[j]])
			l := slices.IndexFunc(laneEnd, func(e int) bool { return e <= f })
			if l < 0 {
				l = len(laneEnd)
				laneEnd = append(laneEnd, 0)
			}
			laneEnd[l] = t
			lanes[j-start] = l
		}
		for j := start; j < stop; j++ {
			set(&items[idx[j]], lanes[j-start], len(laneEnd))
		}
		start = stop
	}
}

// occurrence draws one block of the week grid: solid when it will run,
// faded when the schedule is off, hatched when the date is skipped.
func (a *App) occurrence(dl *imgui.DrawList, p0, p1 imgui.Vec2, col color.RGBA, enabled, skipped bool) {
	switch {
	case skipped:
		dl.AddRectV(p0, p1, u32(alpha(col, 120)), 3, imgui.DrawFlagsRoundCornersAll, 1)
		dl.PushClipRectV(p0, p1, true)
		for x := p0.X - (p1.Y - p0.Y); x < p1.X; x += 6 {
			dl.AddLineV(vec(x, p1.Y), vec(x+(p1.Y-p0.Y), p0.Y), u32(alpha(col, 70)), 1)
		}
		dl.PopClipRect()
	case !enabled:
		dl.AddRectFilledV(p0, p1, u32(alpha(col, 45)), 3, imgui.DrawFlagsRoundCornersAll)
	default:
		dl.AddRectFilledV(p0, p1, u32(alpha(col, 215)), 3, imgui.DrawFlagsRoundCornersAll)
	}
}

// ── schedule cards ─────────────────────────────────────────────────────

// scheduleStatus is the one-line state of a schedule and its color.
func scheduleStatus(v api.ScheduleView, now time.Time) (string, color.RGBA) {
	switch {
	case !v.Enabled:
		return "Desligada", colFaint
	case v.Active && v.Pause != nil:
		return "Valendo até " + domain.When(v.Current.End, now) + "  ·  pausa às " + domain.When(v.Pause.From, now), colWarn
	case v.Active:
		return "Valendo até " + domain.When(v.Current.End, now), colBlock
	case v.Current != nil && v.Pause != nil:
		return "Pausada até " + domain.When(v.Pause.Until, now), colWarn
	case v.Next != nil:
		return "Próxima: " + domain.When(v.Next.Start, now), colMuted
	}
	return "Sem horários pela frente", colFaint
}

// guarded means switching the schedule off now goes through its guard.
func guarded(v api.ScheduleView) bool { return v.Active && v.Guard.Mode != domain.GuardFree }

func (a *App) scheduleCard(s snapshot, v api.ScheduleView, col color.RGBA, w float32, now time.Time, pending map[string]bool) {
	const h = 168
	key := "sched:" + v.ID
	on := v.Enabled
	if p, ok := pending[key]; ok {
		on = p
	}
	bg := colSurface
	if v.Active {
		bg = mix(colSurface, colBlock, 0.07)
	}
	pos := imgui.CursorScreenPos()
	card("scard/"+v.ID, w, h, bg, func() {
		dl := imgui.WindowDrawList()
		inner := imgui.ContentRegionAvail().X
		top := imgui.CursorScreenPos()

		// Title row: color, name, mode, toggle.
		dl.AddRectFilledV(vec(top.X, top.Y+5), vec(top.X+12, top.Y+17), u32(col), 4, imgui.DrawFlagsRoundCornersAll)
		mode := modeName(v.Guard.Mode)
		if v.Guard.Mode == domain.GuardFriction {
			mode += fmt.Sprintf(" · %d min", v.Guard.DelayMinutes)
		}
		imgui.SetCursorScreenPos(vec(top.X+20, top.Y))
		a.bold(17, func() { text(ellipsize(v.Name, inner-20-60-a.pillWidth(mode)-8), colText) })
		imgui.SameLineV(0, 8)
		imgui.SetCursorPosY(imgui.CursorPosY() + 2)
		mc := modeColor(v.Guard.Mode)
		a.pill(mode, mc, alpha(mc, 30))
		imgui.SetCursorScreenPos(vec(top.X+inner-42, top.Y))
		lockedNow := guarded(v)
		if a.toggle("stoggle/"+v.ID, on, !lockedNow || !on, col) {
			id, name := v.ID, v.Name
			if on {
				a.do(key, false, name+" desligada.", func(ctx ctxT) error { return a.ctl.ScheduleEnable(ctx, id, false) })
			} else {
				a.do(key, true, name+" ligada.", func(ctx ctxT) error { return a.ctl.ScheduleEnable(ctx, id, true) })
			}
		}
		if lockedNow && on {
			tooltip(modeName(v.Guard.Mode) + ": só dá para desligar depois de " + domain.When(v.Current.End, now) + ".")
		}

		// Status and windows.
		status, sc := scheduleStatus(v, now)
		imgui.SetCursorScreenPos(vec(top.X, top.Y+32))
		a.medium(13.5, func() { text(ellipsize(status, inner), sc) })
		imgui.SetCursorScreenPos(vec(top.X, top.Y+54))
		var ws []string
		for _, win := range v.Windows {
			ws = append(ws, windowLabel(win))
		}
		a.font(nil, 12.5, func() { text(ellipsize(strings.Join(ws, "   ·   "), inner), colMuted) })

		// Profiles, one line.
		imgui.SetCursorScreenPos(vec(top.X, top.Y+80))
		x := float32(0)
		for i, id := range v.Profiles {
			label := profileName(s, id)
			pw := a.pillWidth(label)
			rest := len(v.Profiles) - i - 1
			if x+pw+float32(min(rest, 1))*44 > inner {
				a.pill(fmt.Sprintf("+%d", rest+1), colMuted, colSurface3)
				break
			}
			if i > 0 {
				imgui.SameLineV(0, 6)
			}
			a.pill(label, colText, colSurface3)
			x += pw + 6
		}

		// Footer actions.
		imgui.SetCursorScreenPos(vec(top.X-6, top.Y+h-32-34))
		id, name := v.ID, v.Name
		if a.button("Editar##"+id, btnGhost, 0) {
			a.schedEd = newScheduleEditor(s, &v)
		}
		imgui.SameLineV(0, 2)
		switch {
		case v.Pause != nil:
			if a.button("Retomar##"+id, btnGhost, 0) {
				a.do("", false, name+" retomada.", func(ctx ctxT) error { return a.ctl.ScheduleResume(ctx, id) })
			}
			imgui.SameLineV(0, 2)
		case v.Active && v.Guard.Mode == domain.GuardLocked:
			imgui.BeginDisabled()
			a.button("Pausar##"+id, btnGhost, 0)
			imgui.EndDisabled()
			tooltip("Modo travado: vale até " + domain.When(v.Current.End, now) + ".")
			imgui.SameLineV(0, 2)
		case v.Active:
			if a.button("Pausar##"+id, btnGhost, 0) {
				if v.Guard.Mode == domain.GuardFriction {
					a.pauseDlg = &pauseDialog{id: id, name: name, guard: v.Guard, until: v.Current.End}
				} else {
					a.do("", false, name+" pausada até "+domain.When(v.Current.End, now)+".", func(ctx ctxT) error {
						return a.ctl.SchedulePause(ctx, id, "")
					})
				}
			}
			imgui.SameLineV(0, 2)
		case v.Enabled && v.Next != nil:
			date := v.Next.Start.Format(domain.DateLayout)
			if a.button("Pular próxima##"+id, btnGhost, 0) {
				a.do("", false, name+" não vai valer em "+v.Next.Start.Format("02/01")+".", func(ctx ctxT) error {
					return a.ctl.ScheduleSkip(ctx, id, date, false)
				})
			}
			tooltip("Pula a janela de " + domain.When(v.Next.Start, now) + ". Desfaça no editor.")
			imgui.SameLineV(0, 2)
		}
		if a.button("Apagar##"+id, btnGhost, 0) {
			a.confirm = &confirmDialog{
				title: "Apagar a agenda " + name + "?",
				body:  "Os perfis dela deixam de ligar sozinhos. Se ela estiver valendo, os apps são liberados (menos os que outra coisa segura).",
				label: "Apagar",
				run: func() {
					a.do("", false, "Agenda "+name+" apagada.", func(ctx ctxT) error { return a.ctl.ScheduleRemove(ctx, id) })
				},
			}
		}
		if n := len(v.Skip); n > 0 {
			label := plural(n, "1 data pulada", "%d datas puladas")
			a.font(nil, 12, func() {
				ts := imgui.CalcTextSize(label)
				dl.AddTextVec2(vec(top.X+inner-ts.X, top.Y+h-32-26), u32(colFaint), label)
			})
		}
	})
	if v.Active {
		dl := imgui.WindowDrawList()
		dl.AddRectV(pos, vec(pos.X+w, pos.Y+h), u32(alpha(colBlock, 110)), 12, imgui.DrawFlagsRoundCornersAll, 1.2)
	}
}

func calendarIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	dl.AddRectV(vec(c.X-7, c.Y-5.5), vec(c.X+7, c.Y+7), u32(col), 2.5, imgui.DrawFlagsRoundCornersAll, 1.6)
	dl.AddLineV(vec(c.X-7, c.Y-1.5), vec(c.X+7, c.Y-1.5), u32(col), 1.6)
	dl.AddLineV(vec(c.X-3.5, c.Y-8), vec(c.X-3.5, c.Y-4), u32(col), 1.8)
	dl.AddLineV(vec(c.X+3.5, c.Y-8), vec(c.X+3.5, c.Y-4), u32(col), 1.8)
	dl.AddRectFilledV(vec(c.X+1, c.Y+1.5), vec(c.X+4.5, c.Y+4.5), u32(col), 1, imgui.DrawFlagsRoundCornersAll)
}
