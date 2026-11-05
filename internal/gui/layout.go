package gui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/AllenDang/cimgui-go/backend/glfwbackend"
	"github.com/AllenDang/cimgui-go/imgui"
	g "github.com/AllenDang/giu"
)

const sidebarWidth = 240

// Run opens the window and blocks until it is closed. Data refresh starts
// only once the window exists (giu.Update needs it).
func (a *App) Run(ctx context.Context) {
	wnd := g.NewMasterWindow("blockd", 1200, 780, 0)
	a.start(ctx)
	wnd.SetSizeLimits(940, 620, -1, -1)
	wnd.SetBgColor(colBg)
	wnd.SetStyle(windowStyle())
	a.theme.install()
	vsyncOff := false
	wnd.Run(func() {
		// The backend loop already caps at 30 fps. With vsync, Mesa blocks
		// eglSwapBuffers while the window is off-screen (no frame callbacks),
		// the loop stops answering compositor pings and Hyprland flags the
		// app as not responding. Set on the first frame: the GL context is
		// current on this thread.
		if !vsyncOff {
			if b, ok := g.Context.Backend().(*g.GLFWBackend); ok {
				_ = b.GLFWBackend.SetSwapInterval(glfwbackend.GLFWSwapIntervalImmediate)
			}
			vsyncOff = true
		}
		g.SingleWindow().Layout(g.Custom(a.draw))
	})
}

func (a *App) draw() {
	snap, pending := a.snapshot()
	h := imgui.ContentRegionAvail().Y
	if snap.loaded && a.openOnLoad != "" {
		switch a.openOnLoad {
		case "new-profile":
			a.editor = newProfileEditor(snap, nil)
		case "new-app":
			a.appForm = &appForm{}
		case "new-schedule":
			a.schedEd = newScheduleEditor(snap, nil)
		case "focus":
			a.focusForm = newFocusForm()
		}
		a.openOnLoad = ""
	}

	a.sidebar(snap, h)
	imgui.SameLineV(0, 0)

	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(40, 32))
	if imgui.BeginChildStrV("main", vec(0, 0), imgui.ChildFlagsAlwaysUseWindowPadding, 0) {
		switch {
		case !snap.loaded:
			a.offline(snap)
		case a.page == pageCatalog:
			a.catalogPage(snap, pending)
		case a.page == pageProfiles:
			a.profilesPage(snap, pending)
		case a.page == pageSchedule:
			a.schedulePage(snap, pending)
		default:
			a.overviewPage(snap, pending)
		}
	}
	imgui.EndChild()
	imgui.PopStyleVarV(1)

	a.dialogs(snap)
	a.drawToasts()
}

// ── sidebar ────────────────────────────────────────────────────────────

func (a *App) sidebar(s snapshot, h float32) {
	imgui.PushStyleColorVec4(imgui.ColChildBg, g.ToVec4Color(colSidebar))
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(18, 24))
	imgui.PushStyleVarFloat(imgui.StyleVarChildRounding, 0)
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, vec(10, 4))
	if imgui.BeginChildStrV("sidebar", vec(sidebarWidth, h), imgui.ChildFlagsAlwaysUseWindowPadding, imgui.WindowFlagsNoScrollbar) {
		dl := imgui.WindowDrawList()

		// Brand.
		pos := imgui.CursorScreenPos()
		dl.AddRectFilledV(pos, vec(pos.X+38, pos.Y+38), u32(colBlock), 11, imgui.DrawFlagsRoundCornersAll)
		banIcon(dl, vec(pos.X+19, pos.Y+19), 9.5, rgb(0xffffff), 2.6)
		imgui.SetCursorScreenPos(vec(pos.X+50, pos.Y-1))
		a.bold(20, func() { text("blockd", colText) })
		imgui.SetCursorScreenPos(vec(pos.X+50, pos.Y+22))
		a.font(nil, 12.5, func() { text("menos tela, mais foco", colMuted) })
		imgui.SetCursorScreenPos(vec(pos.X, pos.Y+70))
		imgui.Dummy(vec(0, 0))

		a.medium(11.5, func() { text("NAVEGAÇÃO", colFaint) })
		imgui.Dummy(vec(0, 4))
		blocked := fmt.Sprint(len(s.status.Blocked))
		active := fmt.Sprint(len(s.status.ActiveProfiles))
		a.navItem(pageOverview, "Visão geral", gridIcon, "")
		a.navItem(pageCatalog, "Catálogo", listIcon, blocked+"/"+fmt.Sprint(len(s.catalog)))
		a.navItem(pageProfiles, "Perfis", layersIcon, active+" ativos")
		running := ""
		if n := len(s.status.ActiveSchedules) + len(s.status.Focus); n > 0 {
			running = plural(n, "1 valendo", "%d valendo")
		}
		a.navItem(pageSchedule, "Agenda", calendarIcon, running)

		// Sync status pinned to the bottom.
		imgui.SetCursorPosY(h - 112)
		a.syncCard(s)
	}
	imgui.EndChild()
	imgui.PopStyleVarV(3)
	imgui.PopStyleColorV(1)
}

func (a *App) navItem(p page, label string, icon func(*imgui.DrawList, imgui.Vec2, color.RGBA), badge string) {
	const h = 42
	w := imgui.ContentRegionAvail().X
	pos := imgui.CursorScreenPos()
	if imgui.InvisibleButton("nav/"+label, vec(w, h)) {
		a.page = p
	}
	hovered := imgui.IsItemHovered()
	handCursorIfHovered()
	active := a.page == p
	dl := imgui.WindowDrawList()
	t := a.anim("nav/"+label, map[bool]float32{false: 0, true: 1}[active])
	if hovered && !active {
		dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(alpha(colSurface2, 140)), 10, imgui.DrawFlagsRoundCornersAll)
	}
	if t > 0 {
		dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(alpha(colSurface2, uint8(255*t))), 10, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectFilledV(vec(pos.X, pos.Y+11), vec(pos.X+3, pos.Y+h-11), u32(alpha(colBlock, uint8(255*t))), 2, imgui.DrawFlagsRoundCornersAll)
	}
	fg := mix(colMuted, colText, t)
	icon(dl, vec(pos.X+22, pos.Y+h/2), fg)
	a.medium(14.5, func() {
		ts := imgui.CalcTextSize(label)
		dl.AddTextVec2(vec(pos.X+42, pos.Y+(h-ts.Y)/2), u32(fg), label)
	})
	if badge != "" {
		a.medium(11.5, func() {
			ts := imgui.CalcTextSize(badge)
			bx := pos.X + w - ts.X - 22
			by := pos.Y + (h-ts.Y-6)/2
			dl.AddRectFilledV(vec(bx, by), vec(bx+ts.X+12, by+ts.Y+6), u32(colSurface3), 8, imgui.DrawFlagsRoundCornersAll)
			dl.AddTextVec2(vec(bx+6, by+3), u32(colMuted), badge)
		})
	}
	imgui.Dummy(vec(0, 0))
}

func (a *App) syncCard(s snapshot) {
	w := imgui.ContentRegionAvail().X
	card("sync", w, 88, colSurface, func() {
		dl := imgui.WindowDrawList()
		pos := imgui.CursorScreenPos()
		var col color.RGBA
		var title, sub string
		switch {
		case !s.loaded:
			col, title, sub = colBlock, "Daemon offline", "tentando reconectar…"
		case s.status.Sync.InSync:
			col, title = colOK, "Sincronizado"
			sub = "checado às " + s.status.Sync.LastCheck.Local().Format("15:04:05")
		default:
			col, title, sub = colBlock, "Fora de sincronia", "veja os detalhes na visão geral"
		}
		if s.loaded && s.status.DryRun {
			sub = "modo dry-run (nada é aplicado)"
			col = colWarn
		}
		dot(dl, vec(pos.X+7, pos.Y+10), col)
		imgui.SetCursorScreenPos(vec(pos.X+22, pos.Y))
		a.medium(14, func() { text(title, colText) })
		imgui.SetCursorScreenPos(vec(pos.X+22, pos.Y+22))
		a.font(nil, 12, func() { text(sub, colMuted) })
		if s.loaded && s.status.Sync.LastDrift != nil {
			imgui.SetCursorScreenPos(vec(pos.X+22, pos.Y+38))
			a.font(nil, 12, func() {
				text("drift corrigido às "+s.status.Sync.LastDrift.Local().Format("15:04"), colWarn)
			})
		}
	})
}

// ── page chrome ────────────────────────────────────────────────────────

// header draws the page title and, when action is set, a right-aligned button.
func (a *App) header(title, subtitle, action string) (clicked bool) {
	startY := imgui.CursorPosY()
	a.bold(27, func() { text(title, colText) })
	a.font(nil, 14.5, func() { text(subtitle, colMuted) })
	endY := imgui.CursorPosY()
	if action != "" {
		bw := a.buttonWidth(action)
		imgui.SetCursorPos(vec(imgui.WindowWidth()-40-bw, startY+8))
		clicked = a.button(action, btnPrimary, 0)
		imgui.SetCursorPosY(endY)
	}
	imgui.Dummy(vec(0, 14))
	return clicked
}

func (a *App) section(title, right string) {
	imgui.Dummy(vec(0, 6))
	a.medium(12, func() { text(strings.ToUpper(title), colFaint) })
	if right != "" {
		imgui.SameLine()
		a.font(nil, 12, func() { text(right, colFaint) })
	}
	imgui.Dummy(vec(0, 2))
}

func (a *App) offline(s snapshot) {
	a.header("Sem conexão com o blockd", "A interface controla o daemon pelo blockctl.", "")
	w := min(imgui.ContentRegionAvail().X, 760)
	card("offline", w, 0, colSurface, func() {
		a.medium(15, func() { text("O que aconteceu", colText) })
		msg := s.connErr
		if msg == "" {
			msg = "carregando…"
		}
		wrapped(msg, colBlock, w-40)
		imgui.Dummy(vec(0, 6))
		a.medium(15, func() { text("Como resolver", colText) })
		for _, l := range []string{
			"sudo systemctl enable --now blockd",
			"sudo gpasswd -a $USER blockd   (depois faça logout/login)",
			"blockctl status   — para ver o erro no terminal",
		} {
			text("  "+l, colMuted)
		}
		imgui.Dummy(vec(0, 6))
		if a.button("Tentar de novo", btnPrimary, 0) {
			a.refreshSoon()
		}
	})
}

// ── toasts ─────────────────────────────────────────────────────────────

func (a *App) drawToasts() {
	ts := a.currentToasts()
	if len(ts) == 0 {
		return
	}
	vp := imgui.MainViewport()
	dl := imgui.ForegroundDrawListViewportPtr()
	y := vp.WorkPos().Y + vp.WorkSize().Y - 24
	right := vp.WorkPos().X + vp.WorkSize().X - 24
	// cimgui-go dereferences the clip rect, so it can't be nil (NULL in C++).
	clip := imgui.Vec4{X: vp.Pos().X, Y: vp.Pos().Y, Z: vp.Pos().X + vp.Size().X, W: vp.Pos().Y + vp.Size().Y}
	for i := len(ts) - 1; i >= 0; i-- {
		t := ts[i]
		col := colOK
		if t.kind == toastErr {
			col = colBlock
		}
		// fade out during the last 400ms
		fade := float32(1)
		if left := time.Until(t.until); left < 400*time.Millisecond {
			fade = max(0, float32(left)/float32(400*time.Millisecond))
		}
		g.Update()
		a.font(nil, 14, func() {
			maxW := float32(420)
			sz := imgui.CalcTextSizeV(t.msg, false, maxW)
			w, h := sz.X+48, sz.Y+26
			p0 := vec(right-w, y-h)
			dl.AddRectFilledV(vec(p0.X, p0.Y+3), vec(right, y+3), u32(color.RGBA{0, 0, 0, uint8(90 * fade)}), 12, imgui.DrawFlagsRoundCornersAll)
			dl.AddRectFilledV(p0, vec(right, y), u32(alpha(colSurface2, uint8(255*fade))), 12, imgui.DrawFlagsRoundCornersAll)
			dl.AddRectFilledV(vec(p0.X, p0.Y), vec(p0.X+4, y), u32(alpha(col, uint8(255*fade))), 12, imgui.DrawFlagsRoundCornersAll)
			dot(dl, vec(p0.X+20, p0.Y+h/2), alpha(col, uint8(255*fade)))
			dl.AddTextFontPtrV(imgui.CurrentFont(), imgui.FontSize(), vec(p0.X+34, p0.Y+13),
				u32(alpha(colText, uint8(255*fade))), t.msg, maxW, &clip)
			y -= h + 10
		})
	}
}
