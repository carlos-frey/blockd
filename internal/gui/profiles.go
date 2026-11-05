package gui

import (
	"fmt"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
)

func (a *App) profilesPage(s snapshot, pending map[string]bool) {
	if a.header("Perfis", "Vários perfis podem ficar ativos: o bloqueio é a união de todos eles.", "+  Novo perfil") {
		a.editor = newProfileEditor(s, nil)
	}
	avail := imgui.ContentRegionAvail().X
	const gap = 16
	cols := 2
	if avail > 1100 {
		cols = 3
	}
	cw := (avail - float32(cols-1)*gap) / float32(cols)

	var ready, mine []api.ProfileView
	for _, p := range s.profiles {
		if p.Builtin {
			ready = append(ready, p)
		} else {
			mine = append(mine, p)
		}
	}
	grid := func(ps []api.ProfileView) {
		for i, p := range ps {
			if i%cols != 0 {
				imgui.SameLineV(0, gap)
			}
			a.profileCard(s, p, cw, pending)
		}
	}
	a.section("Seus perfis", fmt.Sprintf("— %d", len(mine)))
	if len(mine) == 0 {
		card("no-mine", avail, 0, colSurface, func() {
			text("Você ainda não criou perfis.", colText)
			text("Crie um do zero ou clone um pronto para ajustar. Os apps bloqueados agora já vêm marcados.", colMuted)
		})
	}
	grid(mine)
	a.section("Prontos", "— clone para personalizar")
	grid(ready)
}

func (a *App) profileCard(s snapshot, p api.ProfileView, w float32, pending map[string]bool) {
	key := "profile:" + p.ID
	on := p.Active
	if v, ok := pending[key]; ok {
		on = v
	}
	const h = 184
	pos := imgui.CursorScreenPos()
	bg := colSurface
	if on {
		bg = mix(colSurface, colBlock, 0.08)
	}
	card("pcard/"+p.ID, w, h, bg, func() {
		dl := imgui.WindowDrawList()
		inner := imgui.ContentRegionAvail().X
		top := imgui.CursorScreenPos()

		// Title row.
		titleW := inner - 60
		if p.Builtin {
			titleW -= a.pillWidth("pronto") + 8
		}
		a.bold(17, func() { text(ellipsize(p.Name, titleW), colText) })
		if p.Builtin {
			imgui.SameLineV(0, 8)
			imgui.SetCursorPosY(imgui.CursorPosY() + 2)
			a.pill("pronto", colMuted, colSurface3)
		}
		imgui.SetCursorScreenPos(vec(top.X+inner-42, top.Y))
		if a.toggle("ptoggle/"+p.ID, on, true, colBlock) {
			a.toggleProfile(p, !on)
		}

		// Description.
		imgui.SetCursorScreenPos(vec(top.X, top.Y+30))
		desc := p.Description
		if desc == "" {
			desc = "Sem descrição."
		}
		a.font(nil, 13, func() { text(ellipsize(desc, inner), colMuted) })

		// App chips, as many as fit in two lines.
		imgui.SetCursorScreenPos(vec(top.X, top.Y+58))
		a.appChips(s, p.Apps, inner, 2)

		// Footer actions.
		imgui.SetCursorScreenPos(vec(top.X-6, top.Y+h-32-34))
		if !p.Builtin {
			if a.button("Editar##"+p.ID, btnGhost, 0) {
				a.editor = newProfileEditor(s, &p)
			}
			imgui.SameLineV(0, 2)
		}
		if a.button("Clonar##"+p.ID, btnGhost, 0) {
			a.do("", false, "Cópia de "+p.Name+" criada.", func(ctx ctxT) error { return a.ctl.ProfileClone(ctx, p.ID) })
		}
		if !p.Builtin {
			imgui.SameLineV(0, 2)
			if a.button("Apagar##"+p.ID, btnGhost, 0) {
				id, name := p.ID, p.Name
				a.confirm = &confirmDialog{
					title: "Apagar o perfil " + name + "?",
					body:  "Se estiver ativo, os apps dele são liberados (menos os que outro perfil ou bloqueio avulso segura).",
					label: "Apagar",
					run: func() {
						a.do("", false, "Perfil "+name+" apagado.", func(ctx ctxT) error { return a.ctl.ProfileRemove(ctx, id) })
					},
				}
			}
		}
		count := plural(len(p.Apps), "1 app", "%d apps")
		a.font(nil, 12.5, func() {
			ts := imgui.CalcTextSize(count)
			dl.AddTextVec2(vec(top.X+inner-ts.X, top.Y+h-32-26), u32(colFaint), count)
		})
	})
	if on {
		dl := imgui.WindowDrawList()
		dl.AddRectV(pos, vec(pos.X+w, pos.Y+h), u32(alpha(colBlock, 120)), 12, imgui.DrawFlagsRoundCornersAll, 1.2)
	}
}

// appChips renders app names as small pills, wrapping up to maxLines; what
// does not fit collapses into a final "+N" pill.
func (a *App) appChips(s snapshot, ids []string, width float32, maxLines int) {
	byID := map[string]api.AppView{}
	for _, v := range s.catalog {
		byID[v.ID] = v
	}
	const gap = 6
	labels := make([]string, len(ids))
	widths := make([]float32, len(ids))
	for i, id := range ids {
		labels[i] = id
		if v, ok := byID[id]; ok {
			labels[i] = v.Name
		}
		widths[i] = a.pillWidth(labels[i])
	}

	// First pass: decide each pill's line, reserving room for "+N" on the last.
	line, x, shown := 0, float32(0), 0
	lines := make([]int, 0, len(ids))
	for i := range ids {
		need := widths[i]
		if x > 0 {
			need += gap
		}
		if line == maxLines-1 && i < len(ids)-1 {
			need += gap + a.pillWidth(fmt.Sprintf("+%d", len(ids)-i-1))
		}
		if x+need > width {
			if line == maxLines-1 {
				break
			}
			line, x = line+1, 0
			need = widths[i]
			if line == maxLines-1 && i < len(ids)-1 {
				need += gap + a.pillWidth(fmt.Sprintf("+%d", len(ids)-i-1))
			}
			if need > width {
				break
			}
		}
		if x > 0 {
			x += gap
		}
		x += widths[i]
		lines = append(lines, line)
		shown++
	}

	startX := imgui.CursorPosX()
	for i := 0; i < shown; i++ {
		switch {
		case i == 0:
		case lines[i] == lines[i-1]:
			imgui.SameLineV(0, gap)
		default:
			imgui.SetCursorPosX(startX)
			imgui.SetCursorPosY(imgui.CursorPosY() + 2)
		}
		fg := colMuted
		if v, ok := byID[ids[i]]; ok {
			fg = mix(categoryColor(v.Category), colText, 0.25)
		}
		a.pill(labels[i], fg, colSurface3)
	}
	if rest := len(ids) - shown; rest > 0 {
		if shown > 0 {
			imgui.SameLineV(0, gap)
		}
		a.pill(fmt.Sprintf("+%d", rest), colMuted, colSurface3)
	}
}
