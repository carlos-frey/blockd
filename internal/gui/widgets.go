package gui

import (
	"image/color"
	"math"

	"github.com/AllenDang/cimgui-go/imgui"
	g "github.com/AllenDang/giu"
)

// Immediate-mode building blocks. They draw with the window draw list and
// reserve space with Dummy/InvisibleButton, so they compose with SameLine.

func (a *App) font(fi *g.FontInfo, size float32, fn func()) {
	pushed := g.PushFont(fi)
	g.PushFontSize(size)
	fn()
	g.PopFont()
	if pushed {
		g.PopFont()
	}
}

func (a *App) medium(size float32, fn func()) { a.font(a.theme.medium, size, fn) }
func (a *App) bold(size float32, fn func())   { a.font(a.theme.bold, size, fn) }

func text(s string, c color.RGBA) {
	imgui.PushStyleColorVec4(imgui.ColText, g.ToVec4Color(c))
	imgui.TextUnformatted(s)
	imgui.PopStyleColorV(1)
}

func wrapped(s string, c color.RGBA, width float32) {
	imgui.PushStyleColorVec4(imgui.ColText, g.ToVec4Color(c))
	imgui.PushTextWrapPosV(imgui.CursorPosX() + width)
	imgui.TextUnformatted(s)
	imgui.PopTextWrapPos()
	imgui.PopStyleColorV(1)
}

func handCursorIfHovered() {
	if imgui.IsItemHovered() {
		imgui.SetMouseCursor(imgui.MouseCursorHand)
	}
}

func tooltip(s string) {
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled|imgui.HoveredFlagsDelayShort) && s != "" {
		imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(10, 8))
		imgui.SetTooltip(s)
		imgui.PopStyleVarV(1)
	}
}

func lerp(a, b, t float32) float32 { return a + (b-a)*t }

func mix(a, b color.RGBA, t float32) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(lerp(float32(x), float32(y), t)) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}

// anim eases a per-id value towards target and keeps frames coming while it moves.
func (a *App) anim(id string, target float32) float32 {
	if a.anims == nil {
		a.anims = map[string]float32{}
	}
	v, ok := a.anims[id]
	if !ok {
		v = target
	}
	v += (target - v) * min(1, imgui.CurrentIO().DeltaTime()*16)
	if math.Abs(float64(target-v)) < 0.01 {
		v = target
	} else {
		g.Update()
	}
	a.anims[id] = v
	return v
}

// toggle is an iOS-style switch; returns true when clicked (and enabled).
func (a *App) toggle(id string, on, enabled bool, onCol color.RGBA) bool {
	const w, h = 42, 24
	pos := imgui.CursorScreenPos()
	clicked := imgui.InvisibleButton(id, vec(w, h)) && enabled
	hovered := imgui.IsItemHovered()
	if hovered && enabled {
		imgui.SetMouseCursor(imgui.MouseCursorHand)
	}
	t := a.anim("tg/"+id, map[bool]float32{false: 0, true: 1}[on])
	track := mix(colSurface3, onCol, t)
	knob := rgb(0xffffff)
	if hovered && enabled {
		track = mix(track, rgb(0xffffff), 0.08)
	}
	if !enabled {
		track = alpha(track, 110)
		knob = alpha(knob, 150)
	}
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(pos, vec(pos.X+w, pos.Y+h), u32(track), h/2, imgui.DrawFlagsRoundCornersAll)
	cx := pos.X + h/2 + t*(w-h)
	dl.AddCircleFilledV(vec(cx, pos.Y+h/2+1), h/2-3, u32(color.RGBA{0, 0, 0, 50}), 24)
	dl.AddCircleFilledV(vec(cx, pos.Y+h/2), h/2-3, u32(knob), 24)
	if !enabled && on {
		lockIcon(dl, vec(cx, pos.Y+h/2), onCol)
	}
	return clicked
}

// pill is a small rounded label.
func (a *App) pill(label string, fg, bg color.RGBA) {
	a.medium(12, func() {
		ts := imgui.CalcTextSize(label)
		const px, py = 9, 3
		pos := imgui.CursorScreenPos()
		size := vec(ts.X+2*px, ts.Y+2*py)
		dl := imgui.WindowDrawList()
		dl.AddRectFilledV(pos, vec(pos.X+size.X, pos.Y+size.Y), u32(bg), size.Y/2, imgui.DrawFlagsRoundCornersAll)
		dl.AddTextVec2(vec(pos.X+px, pos.Y+py), u32(fg), label)
		imgui.Dummy(size)
	})
}

// pillWidth predicts the width pill(label) will take.
func (a *App) pillWidth(label string) (w float32) {
	a.medium(12, func() { w = imgui.CalcTextSize(label).X + 18 })
	return w
}

type btnKind int

const (
	btnSecondary btnKind = iota
	btnPrimary
	btnGhost
	btnDanger
)

func (a *App) button(label string, kind btnKind, width float32) bool {
	var base, hover, active, fg color.RGBA
	switch kind {
	case btnPrimary:
		base, hover, active, fg = colAccent, colAccentHi, colAccent, rgb(0xffffff)
	case btnGhost:
		base, hover, active, fg = color.RGBA{}, colSurface2, colSurface3, colMuted
	case btnDanger:
		base, hover, active, fg = alpha(colBlock, 28), alpha(colBlock, 60), alpha(colBlock, 80), colBlock
	default:
		base, hover, active, fg = colSurface2, colSurface3, colBorder, colText
	}
	imgui.PushStyleColorVec4(imgui.ColButton, g.ToVec4Color(base))
	imgui.PushStyleColorVec4(imgui.ColButtonHovered, g.ToVec4Color(hover))
	imgui.PushStyleColorVec4(imgui.ColButtonActive, g.ToVec4Color(active))
	imgui.PushStyleColorVec4(imgui.ColText, g.ToVec4Color(fg))
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding, vec(14, 8))
	var clicked bool
	a.medium(14, func() { clicked = imgui.ButtonV(label, vec(width, 0)) })
	handCursorIfHovered()
	imgui.PopStyleVarV(1)
	imgui.PopStyleColorV(4)
	return clicked
}

// buttonWidth predicts a button's natural width.
func (a *App) buttonWidth(label string) (w float32) {
	a.medium(14, func() { w = imgui.CalcTextSizeV(label, true, -1).X + 28 })
	return w
}

// card opens a padded, rounded surface; pass h=0 to fit the content.
func card(id string, w, h float32, bg color.RGBA, fn func()) {
	imgui.PushStyleColorVec4(imgui.ColChildBg, g.ToVec4Color(bg))
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, vec(18, 16))
	flags := imgui.ChildFlagsAlwaysUseWindowPadding
	if h == 0 {
		flags |= imgui.ChildFlagsAutoResizeY
	}
	if imgui.BeginChildStrV(id, vec(w, h), flags, imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse) {
		fn()
	}
	imgui.EndChild()
	imgui.PopStyleVarV(1)
	imgui.PopStyleColorV(1)
}

// avatar draws a colored rounded square with the app's initial.
func (a *App) avatar(name string, c color.RGBA, size float32) {
	pos := imgui.CursorScreenPos()
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(pos, vec(pos.X+size, pos.Y+size), u32(alpha(c, 38)), size*0.3, imgui.DrawFlagsRoundCornersAll)
	initial := "?"
	for _, r := range name {
		initial = string(r)
		break
	}
	a.bold(size*0.45, func() {
		ts := imgui.CalcTextSize(initial)
		dl.AddTextVec2(vec(pos.X+(size-ts.X)/2, pos.Y+(size-ts.Y)/2), u32(c), initial)
	})
	imgui.Dummy(vec(size, size))
}

// dot is a status light with a soft halo.
func dot(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	dl.AddCircleFilledV(c, 7, u32(alpha(col, 50)), 20)
	dl.AddCircleFilledV(c, 4, u32(col), 20)
}

// ── vector icons (16px box centered on c) ──────────────────────────────

func banIcon(dl *imgui.DrawList, c imgui.Vec2, r float32, col color.RGBA, thick float32) {
	dl.AddCircleV(c, r, u32(col), 32, thick)
	d := r * 0.7071
	dl.AddLineV(vec(c.X-d, c.Y-d), vec(c.X+d, c.Y+d), u32(col), thick)
}

func gridIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	for _, o := range [][2]float32{{-7, -7}, {1, -7}, {-7, 1}, {1, 1}} {
		p := vec(c.X+o[0], c.Y+o[1])
		dl.AddRectFilledV(p, vec(p.X+6, p.Y+6), u32(col), 1.5, imgui.DrawFlagsRoundCornersAll)
	}
}

func listIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	for _, y := range []float32{-5, 0, 5} {
		dl.AddCircleFilledV(vec(c.X-6, c.Y+y), 1.6, u32(col), 8)
		dl.AddLineV(vec(c.X-2, c.Y+y), vec(c.X+7, c.Y+y), u32(col), 1.8)
	}
}

func layersIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	for i, y := range []float32{-4, 0, 4} {
		w := float32(7)
		p := []imgui.Vec2{vec(c.X-w, c.Y+y), vec(c.X, c.Y+y-3.5), vec(c.X+w, c.Y+y), vec(c.X, c.Y+y+3.5)}
		if i == 0 {
			dl.AddQuadFilled(p[0], p[1], p[2], p[3], u32(col))
		} else {
			dl.AddLineV(p[0], p[3], u32(col), 1.6)
			dl.AddLineV(p[3], p[2], u32(col), 1.6)
		}
	}
}

func lockIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	dl.AddRectFilledV(vec(c.X-4, c.Y-1), vec(c.X+4, c.Y+5), u32(col), 1.5, imgui.DrawFlagsRoundCornersAll)
	dl.PathArcToV(vec(c.X, c.Y-1.5), 2.6, math.Pi, 2*math.Pi, 12)
	dl.PathStrokeV(u32(col), 0, 1.5)
}

func checkIcon(dl *imgui.DrawList, c imgui.Vec2, col color.RGBA) {
	dl.AddLineV(vec(c.X-4, c.Y), vec(c.X-1, c.Y+3), u32(col), 2)
	dl.AddLineV(vec(c.X-1, c.Y+3), vec(c.X+4.5, c.Y-3.5), u32(col), 2)
}

// checkbox is a custom square check; returns true when toggled.
func (a *App) checkbox(id string, on bool, accent color.RGBA) bool {
	const s = 18
	pos := imgui.CursorScreenPos()
	clicked := imgui.InvisibleButton(id, vec(s, s))
	handCursorIfHovered()
	dl := imgui.WindowDrawList()
	if on {
		dl.AddRectFilledV(pos, vec(pos.X+s, pos.Y+s), u32(accent), 5, imgui.DrawFlagsRoundCornersAll)
		checkIcon(dl, vec(pos.X+s/2, pos.Y+s/2), rgb(0xffffff))
	} else {
		bg := colSurface2
		if imgui.IsItemHovered() {
			bg = colSurface3
		}
		dl.AddRectFilledV(pos, vec(pos.X+s, pos.Y+s), u32(bg), 5, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectV(pos, vec(pos.X+s, pos.Y+s), u32(colFaint), 5, imgui.DrawFlagsRoundCornersAll, 1.2)
	}
	return clicked
}

// input wraps giu's InputText with consistent styling.
func input(id string, value *string, hint string, width float32) {
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding, vec(12, 9))
	g.InputText(value).Label("##" + id).Hint(hint).Size(width).Build()
	imgui.PopStyleVarV(1)
}
