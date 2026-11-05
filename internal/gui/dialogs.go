package gui

import (
	"context"
	"slices"
	"strings"

	"github.com/AllenDang/cimgui-go/imgui"

	"github.com/carlos-frey/blockd/internal/api"
)

type ctxT = context.Context

type profileEditor struct {
	id         string // empty when creating
	name, desc string
	selected   map[string]bool
	blockedNow []string
	filter     string
	activate   bool
	opened     bool
}

// newProfileEditor starts from the profile being edited or, for a new one,
// from everything that is blocked right now.
func newProfileEditor(s snapshot, p *api.ProfileView) *profileEditor {
	e := &profileEditor{selected: map[string]bool{}}
	for _, v := range s.status.Blocked {
		e.blockedNow = append(e.blockedNow, v.ID)
	}
	if p != nil {
		e.id, e.name, e.desc = p.ID, p.Name, p.Description
		for _, id := range p.Apps {
			e.selected[id] = true
		}
		return e
	}
	for _, id := range e.blockedNow {
		e.selected[id] = true
	}
	return e
}

func (e *profileEditor) apps() []string {
	var out []string
	for id, on := range e.selected {
		if on {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

type appForm struct {
	name, category, domains, processes string
	opened                             bool
}

type confirmDialog struct {
	title, body, label string
	run                func()
	opened             bool
}

func splitInput(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// modal opens (once) and draws a centered modal; fn returns false to close.
func modal(name string, opened *bool, w, h float32, fn func() bool) {
	if !*opened {
		imgui.OpenPopupStr(name)
		*opened = true
	}
	vp := imgui.MainViewport()
	size := vp.WorkSize()
	imgui.SetNextWindowSizeV(vec(min(w, size.X-60), min(h, size.Y-60)), imgui.CondAlways)
	imgui.SetNextWindowPosV(vp.Center(), imgui.CondAlways, vec(0.5, 0.5))
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(30, 26))
	open := imgui.BeginPopupModalV(name, nil, imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove)
	imgui.PopStyleVarV(1)
	if !open {
		return
	}
	keep := fn()
	if imgui.IsKeyPressedBool(imgui.KeyEscape) {
		keep = false
	}
	if !keep {
		imgui.CloseCurrentPopup()
	}
	imgui.EndPopup()
	if !keep {
		*opened = false
	}
}

func (a *App) dialogs(s snapshot) {
	a.scheduleDialogs(s)
	if e := a.editor; e != nil {
		modal("profile-editor", &e.opened, 860, 680, func() bool {
			keep := a.profileEditorBody(s, e)
			if !keep {
				a.editor = nil
			}
			return keep
		})
	}
	if f := a.appForm; f != nil {
		modal("app-form", &f.opened, 560, 520, func() bool {
			keep := a.appFormBody(s, f)
			if !keep {
				a.appForm = nil
			}
			return keep
		})
	}
	if c := a.confirm; c != nil {
		modal("confirm", &c.opened, 460, 200, func() bool {
			a.bold(19, func() { text(c.title, colText) })
			wrapped(c.body, colMuted, 400)
			imgui.SetCursorPosY(imgui.WindowSize().Y - 26 - 36)
			keep := true
			right := imgui.WindowWidth() - 30
			cw := a.buttonWidth("Cancelar")
			lw := a.buttonWidth(c.label)
			imgui.SetCursorPosX(right - cw - lw - 8)
			if a.button("Cancelar", btnSecondary, 0) {
				keep = false
			}
			imgui.SameLineV(0, 8)
			if a.button(c.label, btnDanger, 0) {
				c.run()
				keep = false
			}
			if !keep {
				a.confirm = nil
			}
			return keep
		})
	}
}

func (a *App) profileEditorBody(s snapshot, e *profileEditor) bool {
	title := "Novo perfil"
	if e.id != "" {
		title = "Editar perfil"
	}
	a.bold(22, func() { text(title, colText) })
	if e.id == "" && len(e.blockedNow) > 0 {
		a.font(nil, 13.5, func() {
			text(plural(len(e.blockedNow), "O app bloqueado agora já vem marcado.", "Os %d apps bloqueados agora já vêm marcados."), colMuted)
		})
	} else {
		a.font(nil, 13.5, func() { text("Escolha os apps que este perfil bloqueia.", colMuted) })
	}
	imgui.Dummy(vec(0, 4))

	w := imgui.ContentRegionAvail().X
	half := (w - 16) / 2
	fieldLabel := func(l string) { a.medium(13, func() { text(l, colMuted) }) }
	// Labels on one line, inputs on the next: keeps both columns aligned.
	startX := imgui.CursorPosX()
	fieldLabel("Nome")
	imgui.SameLineV(startX+half+16, 0)
	fieldLabel("Descrição")
	input("pe-name", &e.name, "ex.: Noite sem tela", half)
	imgui.SameLineV(0, 16)
	input("pe-desc", &e.desc, "opcional", half)

	imgui.Dummy(vec(0, 4))
	sel := e.apps()
	a.medium(14.5, func() { text("Apps  ·  "+plural(len(sel), "1 selecionado", "%d selecionados"), colText) })
	input("pe-filter", &e.filter, "Filtrar…", 240)
	imgui.SameLineV(0, 8)
	if a.button("Marcar bloqueados agora", btnSecondary, 0) {
		for _, id := range e.blockedNow {
			e.selected[id] = true
		}
	}
	imgui.SameLineV(0, 8)
	if a.button("Limpar", btnGhost, 0) {
		e.selected = map[string]bool{}
	}

	// Scrollable checklist grouped by category.
	imgui.PushStyleColorVec4(imgui.ColChildBg, gvec(colBg))
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(14, 10))
	if imgui.BeginChildStrV("pe-apps", vec(0, -64), imgui.ChildFlagsAlwaysUseWindowPadding, 0) {
		cw := imgui.ContentRegionAvail().X
		const cols, gap = 3, 8
		colW := (cw - (cols-1)*gap) / cols
		for _, cat := range categories(s.catalog) {
			var apps []api.AppView
			for _, v := range s.catalog {
				if v.Category == cat && appMatches(v, e.filter) {
					apps = append(apps, v)
				}
			}
			if len(apps) == 0 {
				continue
			}
			all := true
			for _, v := range apps {
				all = all && e.selected[v.ID]
			}
			imgui.Dummy(vec(0, 2))
			a.medium(12, func() { text(strings.ToUpper(cat), colFaint) })
			imgui.SameLineV(0, 10)
			linkLabel := "marcar todos"
			if all {
				linkLabel = "desmarcar"
			}
			a.font(nil, 12, func() {
				text(linkLabel, colAccentHi)
				handCursorIfHovered()
				if imgui.IsItemClicked() {
					for _, v := range apps {
						e.selected[v.ID] = !all
					}
				}
			})
			startX := imgui.CursorPosX()
			for i, v := range apps {
				if i%cols != 0 {
					imgui.SameLineV(0, 0)
					imgui.SetCursorPosX(startX + float32(i%cols)*(colW+gap))
				}
				a.appCheckCell(v, colW, e)
			}
		}
	}
	imgui.EndChild()
	imgui.PopStyleVarV(1)
	imgui.PopStyleColorV(1)

	// Footer.
	imgui.Dummy(vec(0, 6))
	keep := true
	if e.id == "" {
		if a.checkbox("pe-activate", e.activate, colBlock) {
			e.activate = !e.activate
		}
		imgui.SameLineV(0, 10)
		text("Ativar ao salvar", colText)
		imgui.SameLine()
	}
	saveLabel := "Criar perfil"
	if e.id != "" {
		saveLabel = "Salvar"
	}
	right := imgui.WindowWidth() - 30
	imgui.SetCursorPosX(right - a.buttonWidth("Cancelar") - a.buttonWidth(saveLabel) - 8)
	if a.button("Cancelar##pe", btnSecondary, 0) {
		keep = false
	}
	imgui.SameLineV(0, 8)
	name := strings.TrimSpace(e.name)
	imgui.BeginDisabledV(name == "")
	if a.button(saveLabel+"##pe", btnPrimary, 0) {
		apps, desc, id, activate := e.apps(), strings.TrimSpace(e.desc), e.id, e.activate
		if id == "" {
			a.do("", false, "Perfil "+name+" criado.", func(ctx ctxT) error {
				return a.ctl.ProfileCreate(ctx, name, desc, apps, activate)
			})
		} else {
			a.do("", false, "Perfil "+name+" salvo.", func(ctx ctxT) error {
				return a.ctl.ProfileUpdate(ctx, id, name, desc, apps)
			})
		}
		keep = false
	}
	imgui.EndDisabled()
	if name == "" {
		tooltip("Dê um nome ao perfil.")
	}
	return keep
}

func (a *App) appCheckCell(v api.AppView, w float32, e *profileEditor) {
	const h = 36
	pos := imgui.CursorScreenPos()
	if imgui.InvisibleButton("cell/"+v.ID, vec(w, h)) {
		e.selected[v.ID] = !e.selected[v.ID]
	}
	hovered := imgui.IsItemHovered()
	handCursorIfHovered()
	dl := imgui.WindowDrawList()
	on := e.selected[v.ID]
	bg := colSurface
	if on {
		bg = mix(colSurface, colBlock, 0.12)
	}
	if hovered {
		bg = mix(bg, colSurface3, 0.6)
	}
	dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(bg), 8, imgui.DrawFlagsRoundCornersAll)
	// check box
	b0 := vec(pos.X+10, pos.Y+(h-16)/2)
	if on {
		dl.AddRectFilledV(b0, vec(b0.X+16, b0.Y+16), u32(colBlock), 4.5, imgui.DrawFlagsRoundCornersAll)
		checkIcon(dl, vec(b0.X+8, b0.Y+8), rgb(0xffffff))
	} else {
		dl.AddRectV(b0, vec(b0.X+16, b0.Y+16), u32(colFaint), 4.5, imgui.DrawFlagsRoundCornersAll, 1.3)
	}
	dl.AddCircleFilledV(vec(pos.X+38, pos.Y+h/2), 3.5, u32(categoryColor(v.Category)), 12)
	a.font(nil, 13.5, func() {
		label := ellipsize(v.Name, w-56)
		ts := imgui.CalcTextSize(label)
		dl.AddTextVec2(vec(pos.X+48, pos.Y+(h-ts.Y)/2), u32(colText), label)
	})
}

func (a *App) appFormBody(s snapshot, f *appForm) bool {
	a.bold(22, func() { text("Novo app", colText) })
	a.font(nil, 13.5, func() {
		text("Adicione algo que não está no catálogo. Pode ter domínios, processos ou ambos.", colMuted)
	})
	imgui.Dummy(vec(0, 6))
	w := imgui.ContentRegionAvail().X
	field := func(label, id string, v *string, hint, help string) {
		a.medium(13, func() { text(label, colMuted) })
		input(id, v, hint, w)
		if help != "" {
			a.font(nil, 12, func() { text(help, colFaint) })
		}
		imgui.Dummy(vec(0, 2))
	}
	field("Nome", "af-name", &f.name, "ex.: Bet365", "")
	field("Categoria", "af-cat", &f.category, "ex.: Apostas", "")
	// Existing categories as quick picks.
	for i, c := range categories(s.catalog) {
		if i > 0 {
			imgui.SameLineV(0, 6)
		}
		if imgui.ContentRegionAvail().X < a.pillWidth(c)+10 {
			imgui.NewLine()
		}
		a.pill(c, colMuted, colSurface3)
		handCursorIfHovered()
		if imgui.IsItemClicked() {
			f.category = c
		}
	}
	imgui.Dummy(vec(0, 4))
	field("Domínios", "af-domains", &f.domains, "bet365.com, bet365.bet.br", "Separe por vírgula. Subdomínios entram junto.")
	field("Processos", "af-procs", &f.processes, "spotify", "Nome do executável que deve ficar sem rede.")

	imgui.SetCursorPosY(imgui.WindowSize().Y - 26 - 36)
	keep := true
	right := imgui.WindowWidth() - 30
	imgui.SetCursorPosX(right - a.buttonWidth("Cancelar") - a.buttonWidth("Adicionar") - 8)
	if a.button("Cancelar##af", btnSecondary, 0) {
		keep = false
	}
	imgui.SameLineV(0, 8)
	name := strings.TrimSpace(f.name)
	domains, procs := splitInput(f.domains), splitInput(f.processes)
	valid := name != "" && len(domains)+len(procs) > 0
	imgui.BeginDisabledV(!valid)
	if a.button("Adicionar##af", btnPrimary, 0) {
		cat := strings.TrimSpace(f.category)
		a.do("", false, name+" adicionado ao catálogo.", func(ctx ctxT) error {
			return a.ctl.AppAdd(ctx, name, cat, domains, procs)
		})
		keep = false
	}
	imgui.EndDisabled()
	if !valid {
		tooltip("Preencha o nome e pelo menos um domínio ou processo.")
	}
	return keep
}
