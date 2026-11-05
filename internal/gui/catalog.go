package gui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// categories returns catalog categories in first-seen order.
func categories(apps []api.AppView) []string {
	var out []string
	for _, a := range apps {
		if !slices.Contains(out, a.Category) {
			out = append(out, a.Category)
		}
	}
	return out
}

func appMatches(v api.AppView, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(v.Name), q) || strings.Contains(v.ID, q) {
		return true
	}
	for _, d := range v.Domains {
		if strings.Contains(d.Name, q) {
			return true
		}
	}
	for _, p := range v.Processes {
		if strings.Contains(strings.ToLower(p), q) {
			return true
		}
	}
	return false
}

func (a *App) catalogPage(s snapshot, pending map[string]bool) {
	if a.header("Catálogo", "Tudo o que o blockd sabe bloquear. Ligue um app para bloqueá-lo avulso.", "+  Novo app") {
		a.appForm = &appForm{}
	}

	input("catalog-search", &a.search, "Buscar por app, domínio ou processo…", 340)
	imgui.SameLineV(0, 16)
	a.categoryChip("Todas", "")
	for _, c := range categories(s.catalog) {
		imgui.SameLineV(0, 6)
		if imgui.ContentRegionAvail().X < a.pillWidth(c)+24 {
			imgui.NewLine()
		}
		a.categoryChip(c, c)
	}
	imgui.Dummy(vec(0, 6))

	w := imgui.ContentRegionAvail().X
	shown := 0
	for _, cat := range categories(s.catalog) {
		if a.category != "" && cat != a.category {
			continue
		}
		var apps []api.AppView
		blocked := 0
		for _, v := range s.catalog {
			if v.Category == cat && appMatches(v, a.search) {
				apps = append(apps, v)
				if v.Blocked {
					blocked++
				}
			}
		}
		if len(apps) == 0 {
			continue
		}
		a.section(cat, fmt.Sprintf("— %d de %d ", blocked, len(apps))+plural(blocked, "bloqueado", "bloqueados"))
		for _, v := range apps {
			a.catalogRow(s, v, w, pending)
			shown++
		}
	}
	if shown == 0 {
		imgui.Dummy(vec(0, 20))
		text("Nada encontrado para essa busca.", colMuted)
	}
}

func (a *App) categoryChip(label, value string) {
	active := a.category == value
	fg, bg := colMuted, colSurface
	if active {
		fg, bg = colText, colSurface3
	}
	pos := imgui.CursorScreenPos()
	pw := a.pillWidth(label)
	// Same height as the search field so the row lines up.
	const h = 34
	if imgui.InvisibleButton("cat/"+label, vec(pw+12, h)) {
		a.category = value
	}
	if imgui.IsItemHovered() && !active {
		bg = colSurface2
	}
	handCursorIfHovered()
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(pos, vec(pos.X+pw+12, pos.Y+h), u32(bg), h/2, imgui.DrawFlagsRoundCornersAll)
	a.medium(13, func() {
		ts := imgui.CalcTextSize(label)
		dl.AddTextVec2(vec(pos.X+(pw+12-ts.X)/2, pos.Y+(h-ts.Y)/2), u32(fg), label)
	})
}

func (a *App) catalogRow(s snapshot, v api.AppView, w float32, pending map[string]bool) {
	const h = 64
	pos := imgui.CursorScreenPos()
	dl := imgui.WindowDrawList()
	hovered := imgui.IsMouseHoveringRect(pos, vec(pos.X+w, pos.Y+h))
	bg := colSurface
	if hovered {
		bg = colSurface2
	}
	dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(bg), 12, imgui.DrawFlagsRoundCornersAll)

	key := "app:" + v.ID
	on := v.Blocked
	if p, ok := pending[key]; ok {
		on = p
	}
	held := heldBy(v)
	if on {
		dl.AddRectFilledV(vec(pos.X, pos.Y+14), vec(pos.X+3, pos.Y+h-14), u32(colBlock), 2, imgui.DrawFlagsRoundCornersAll)
	}

	imgui.SetCursorScreenPos(vec(pos.X+16, pos.Y+(h-36)/2))
	a.avatar(v.Name, categoryColor(v.Category), 36)

	// Right side, laid out right to left: toggle, remove, pills.
	x := pos.X + w - 16 - 42
	imgui.SetCursorScreenPos(vec(x, pos.Y+(h-24)/2))
	enabled := len(held) == 0
	if a.toggle("app/"+v.ID, on, enabled, colBlock) {
		if on {
			a.do(key, false, v.Name+" liberado.", func(ctx ctxT) error { return a.ctl.Unblock(ctx, v.ID) })
		} else {
			a.do(key, true, v.Name+" bloqueado.", func(ctx ctxT) error { return a.ctl.Block(ctx, v.ID) })
		}
	}
	if !enabled {
		names := make([]string, len(held))
		for i, r := range held {
			names[i] = reasonLabel(s, r)
		}
		tooltip("Bloqueado por " + strings.Join(names, ", ") + ".\nDesligue isso para liberar.")
	}
	x -= 12
	if !v.Builtin && hovered {
		bw := a.buttonWidth("Remover")
		x -= bw
		imgui.SetCursorScreenPos(vec(x, pos.Y+(h-34)/2))
		if a.button("Remover##"+v.ID, btnGhost, 0) {
			id, name := v.ID, v.Name
			a.confirm = &confirmDialog{
				title: "Remover " + name + "?",
				body:  "O app sai do catálogo e de todos os seus perfis.",
				label: "Remover",
				run: func() {
					a.do("", false, name+" removido do catálogo.", func(ctx ctxT) error { return a.ctl.AppRemove(ctx, id) })
				},
			}
		}
		x -= 8
	}
	var reasons []string
	if on {
		reasons = v.BlockedBy
	}
	for i := len(reasons) - 1; i >= 0; i-- {
		label := reasonLabel(s, reasons[i])
		pw := a.pillWidth(label)
		x -= pw
		imgui.SetCursorScreenPos(vec(x, pos.Y+(h-22)/2))
		fg, pbg := reasonColors(s, reasons[i])
		a.pill(label, fg, pbg)
		x -= 6
	}
	if !v.Builtin {
		pw := a.pillWidth("custom")
		x -= pw
		imgui.SetCursorScreenPos(vec(x, pos.Y+(h-22)/2))
		a.pill("custom", colAccentHi, alpha(colAccent, 36))
		x -= 6
	}

	// Name and what it covers, clipped to the space left.
	textW := x - (pos.X + 66) - 12
	imgui.SetCursorScreenPos(vec(pos.X+66, pos.Y+12))
	a.medium(15, func() { text(ellipsize(v.Name, textW), colText) })
	imgui.SetCursorScreenPos(vec(pos.X+66, pos.Y+35))
	a.font(nil, 12.5, func() { text(ellipsize(coverage(v), textW), colMuted) })

	imgui.SetCursorScreenPos(vec(pos.X, pos.Y+h-2))
	imgui.Dummy(vec(0, 0))
}

// heldBy lists the reasons other than a manual block keeping v blocked:
// profiles, schedules and focus sessions.
func heldBy(v api.AppView) []string {
	var out []string
	for _, r := range v.BlockedBy {
		if r != domain.ReasonManual {
			out = append(out, r)
		}
	}
	return out
}

// coverage summarizes what blocking the app touches.
func coverage(v api.AppView) string {
	var parts []string
	if n := len(v.Domains); n > 0 {
		d := v.Domains[0].Name
		if n > 1 {
			d += fmt.Sprintf(" +%d", n-1)
		}
		parts = append(parts, d)
	}
	if len(v.Processes) > 0 {
		parts = append(parts, "processo "+v.Processes[0])
	}
	if len(v.IPs) > 0 {
		parts = append(parts, fmt.Sprintf("%d IPs fixos", len(v.IPs)))
	}
	if v.Description != "" {
		parts = append(parts, v.Description)
	}
	return strings.Join(parts, "  ·  ")
}
