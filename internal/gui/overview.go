package gui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// reasonLabel turns a BlockedBy reason into a display name: the profile's
// name, "Agenda Trabalho", "Foco" or "Avulso".
func reasonLabel(s snapshot, r string) string {
	if id, ok := strings.CutPrefix(r, "schedule:"); ok {
		return "Agenda " + scheduleName(s, id)
	}
	if _, ok := strings.CutPrefix(r, "focus:"); ok {
		return "Foco"
	}
	id, ok := strings.CutPrefix(r, "profile:")
	if !ok {
		return "Avulso"
	}
	return profileName(s, id)
}

func (a *App) overviewPage(s snapshot, pending map[string]bool) {
	st := s.status
	sub := "Nada bloqueado agora. Ative um perfil ou bloqueie apps no catálogo."
	if n := len(st.Blocked); n > 0 {
		sub = plural(n, "1 app bloqueado agora", "%d apps bloqueados agora") + " · o firewall segue o que você vê aqui."
	}
	a.header("Visão geral", sub, "")

	if !st.Sync.InSync {
		a.banner("Fora de sincronia", st.Sync.LastError, colBlock, "Sincronizar")
	}
	a.agendaStrip(s)

	// Stat tiles.
	avail := imgui.ContentRegionAvail().X
	const gap = 16
	tw := (avail - 2*gap) / 3
	manual := len(st.ManualApps)
	a.statTile("st-blocked", tw, fmt.Sprint(len(st.Blocked)), plural(len(st.Blocked), "app bloqueado", "apps bloqueados"),
		plural(len(st.Plan.DNSDomains), "1 domínio", "%d domínios")+" · "+plural(manual, "1 avulso", "%d avulsos"), colBlock)
	imgui.SameLineV(0, gap)
	names := []string{}
	for _, p := range s.profiles {
		if p.Active || len(p.ActiveBy) > 0 {
			names = append(names, p.Name)
		}
	}
	activeSub := "nenhum ativo"
	if len(names) > 0 {
		activeSub = ellipsize(strings.Join(names, ", "), tw-40)
	}
	a.statTile("st-profiles", tw, fmt.Sprint(len(names)), plural(len(names), "perfil ativo", "perfis ativos"), activeSub, colAccent)
	imgui.SameLineV(0, gap)
	jailSub := "nenhum app desktop isolado"
	if len(st.Jailed) > 0 {
		seen := map[string]bool{}
		var procs []string
		for _, j := range st.Jailed {
			if !seen[j.Name] {
				seen[j.Name] = true
				procs = append(procs, j.Name)
			}
		}
		jailSub = ellipsize(strings.Join(procs, ", "), tw-40)
	}
	a.statTile("st-jailed", tw, fmt.Sprint(len(st.Jailed)), plural(len(st.Jailed), "processo sem rede", "processos sem rede"), jailSub, colWarn)

	// Quick profile toggles.
	a.section("Perfis", "— clique para ligar ou desligar")
	cols := 3
	cw := (avail - float32(cols-1)*gap) / float32(cols)
	for i, p := range s.profiles {
		if i%cols != 0 {
			imgui.SameLineV(0, gap)
		}
		a.profileChip(s, p, cw, pending)
	}

	// What is blocked and why.
	a.section("Bloqueado agora", fmt.Sprintf("— %d", len(st.Blocked)))
	if len(st.Blocked) == 0 {
		card("empty-blocked", avail, 0, colSurface, func() {
			text("Tudo liberado.", colText)
			text("Os apps aparecem aqui assim que um perfil ou bloqueio avulso os incluir.", colMuted)
		})
		return
	}
	for _, v := range st.Blocked {
		a.blockedRow(s, v, avail)
	}
}

// activatedBy says what switched a profile on: "ligado pela agenda X".
func activatedBy(s snapshot, r string) string {
	if id, ok := strings.CutPrefix(r, "schedule:"); ok {
		return "ligado pela agenda " + scheduleName(s, id)
	}
	return "ligado pelo foco"
}

// reasonColors picks a pill's colors: manual blocks in amber, schedules in
// their own color, focus sessions in coral, profiles neutral.
func reasonColors(s snapshot, r string) (fg, bg color.RGBA) {
	switch {
	case r == domain.ReasonManual:
		return colWarn, alpha(colWarn, 30)
	case strings.HasPrefix(r, "focus:"):
		return colBlock, alpha(colBlock, 30)
	case strings.HasPrefix(r, "schedule:"):
		id := strings.TrimPrefix(r, "schedule:")
		for i, v := range s.schedules {
			if v.ID == id {
				c := scheduleColor(i)
				return mix(c, colText, 0.2), alpha(c, 34)
			}
		}
	}
	return colMuted, colSurface3
}

func (a *App) banner(title, msg string, col color.RGBA, action string) {
	w := imgui.ContentRegionAvail().X
	card("banner", w, 0, alpha(col, 26), func() {
		a.medium(14.5, func() { text(title, col) })
		wrapped(msg, colText, w-220)
		if action != "" {
			imgui.SameLineV(w-36-a.buttonWidth(action), 0)
			if a.button(action, btnSecondary, 0) {
				a.do("", false, "Firewall e DNS reaplicados.", a.ctl.Sync)
			}
		}
	})
	imgui.Dummy(vec(0, 4))
}

// agendaStrip sums up what the schedules and focus sessions are doing and
// when the blocked set changes next.
func (a *App) agendaStrip(s snapshot) {
	st := s.status
	if len(st.Focus) == 0 && len(st.ActiveSchedules) == 0 && st.NextChange == nil {
		return
	}
	now := time.Now()
	var parts []string
	for _, id := range st.ActiveSchedules {
		for _, v := range s.schedules {
			if v.ID == id && v.Current != nil {
				parts = append(parts, v.Name+" valendo até "+domain.When(v.Current.End, now))
			}
		}
	}
	for _, f := range st.Focus {
		parts = append(parts, "foco até "+domain.When(f.Until, now))
	}
	title := "Agenda"
	if len(parts) > 0 {
		title = strings.Join(parts, "  ·  ")
	}
	sub := "Nada agendado pela frente."
	if st.NextChange != nil {
		sub = "Próxima mudança automática: " + domain.When(*st.NextChange, now)
	}
	w := imgui.ContentRegionAvail().X
	card("agenda-strip", w, 64, colSurface, func() {
		dl := imgui.WindowDrawList()
		pos := imgui.CursorScreenPos()
		col := colAccent
		if len(parts) > 0 {
			col = colBlock
		}
		calendarIcon(dl, vec(pos.X+10, pos.Y+15), col)
		bw := a.buttonWidth("Ver agenda")
		imgui.SetCursorScreenPos(vec(pos.X+32, pos.Y-2))
		a.medium(14.5, func() { text(ellipsize(title, w-90-bw), colText) })
		imgui.SetCursorScreenPos(vec(pos.X+32, pos.Y+18))
		a.font(nil, 12.5, func() { text(sub, colMuted) })
		imgui.SetCursorScreenPos(vec(pos.X+w-36-bw, pos.Y+1))
		if a.button("Ver agenda", btnSecondary, 0) {
			a.page = pageSchedule
		}
	})
	imgui.Dummy(vec(0, 4))
}

func (a *App) statTile(id string, w float32, value, label, sub string, col color.RGBA) {
	card(id, w, 118, colSurface, func() {
		dl := imgui.WindowDrawList()
		pos := imgui.CursorScreenPos()
		dl.AddRectFilledV(vec(pos.X, pos.Y+4), vec(pos.X+4, pos.Y+40), u32(col), 2, imgui.DrawFlagsRoundCornersAll)
		imgui.SetCursorScreenPos(vec(pos.X+16, pos.Y-4))
		a.bold(34, func() { text(value, colText) })
		imgui.SetCursorScreenPos(vec(pos.X+16, pos.Y+42))
		a.medium(13.5, func() { text(label, colText) })
		imgui.SetCursorScreenPos(vec(pos.X+16, pos.Y+62))
		a.font(nil, 12.5, func() { text(sub, colMuted) })
	})
}

func (a *App) profileChip(snap snapshot, p api.ProfileView, w float32, pending map[string]bool) {
	key := "profile:" + p.ID
	on := p.Active
	if v, ok := pending[key]; ok {
		on = v
	}
	bg := colSurface
	if on || len(p.ActiveBy) > 0 {
		bg = mix(colSurface, colBlock, 0.10)
	}
	card("chip/"+p.ID, w, 64, bg, func() {
		pos := imgui.CursorScreenPos()
		cw := imgui.ContentRegionAvail().X
		imgui.SetCursorScreenPos(vec(pos.X, pos.Y-3))
		a.medium(14.5, func() { text(ellipsize(p.Name, cw-60), colText) })
		imgui.SetCursorScreenPos(vec(pos.X, pos.Y+17))
		a.font(nil, 12.5, func() {
			if len(p.ActiveBy) > 0 {
				text(ellipsize(activatedBy(snap, p.ActiveBy[0]), cw-60), colBlock)
			} else {
				text(plural(len(p.Apps), "1 app", "%d apps"), colMuted)
			}
		})
		imgui.SetCursorScreenPos(vec(pos.X+cw-42, pos.Y+4))
		if a.toggle("chip-toggle/"+p.ID, on, true, colBlock) {
			a.toggleProfile(p, !on)
		}
	})
}

func (a *App) toggleProfile(p api.ProfileView, on bool) {
	key := "profile:" + p.ID
	if on {
		a.do(key, true, p.Name+" ativado.", func(ctx ctxT) error { return a.ctl.ProfileOn(ctx, p.ID) })
	} else {
		a.do(key, false, p.Name+" desativado.", func(ctx ctxT) error { return a.ctl.ProfileOff(ctx, p.ID) })
	}
}

func (a *App) blockedRow(s snapshot, v api.AppView, w float32) {
	const h = 52
	pos := imgui.CursorScreenPos()
	dl := imgui.WindowDrawList()
	bg := colSurface
	if imgui.IsMouseHoveringRect(pos, vec(pos.X+w, pos.Y+h)) {
		bg = colSurface2
	}
	dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(bg), 10, imgui.DrawFlagsRoundCornersAll)
	imgui.SetCursorScreenPos(vec(pos.X+12, pos.Y+(h-30)/2))
	a.avatar(v.Name, categoryColor(v.Category), 30)
	imgui.SetCursorScreenPos(vec(pos.X+54, pos.Y+(h-20)/2))
	a.medium(14.5, func() { text(v.Name, colText) })

	// Reasons, right aligned.
	x := pos.X + w - 14
	for i := len(v.BlockedBy) - 1; i >= 0; i-- {
		label := reasonLabel(s, v.BlockedBy[i])
		pw := a.pillWidth(label)
		x -= pw
		imgui.SetCursorScreenPos(vec(x, pos.Y+(h-22)/2))
		fg, bgc := reasonColors(s, v.BlockedBy[i])
		a.pill(label, fg, bgc)
		x -= 6
	}
	imgui.SetCursorScreenPos(vec(pos.X, pos.Y+h-2))
	imgui.Dummy(vec(0, 0))
}

// ellipsize trims s with "…" to fit maxW at the current font.
func ellipsize(s string, maxW float32) string {
	if imgui.CalcTextSize(s).X <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if imgui.CalcTextSize(string(r)+"…").X <= maxW {
			break
		}
	}
	return strings.TrimSpace(string(r)) + "…"
}

// plural picks the singular form for n == 1; many may contain one %d.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	if strings.Contains(many, "%d") {
		return fmt.Sprintf(many, n)
	}
	return many
}
