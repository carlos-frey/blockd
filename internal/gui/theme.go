package gui

import (
	_ "embed"
	"image/color"

	"github.com/AllenDang/cimgui-go/imgui"
	g "github.com/AllenDang/giu"
)

var (
	//go:embed fonts/NotoSans-Regular.ttf
	fontRegular []byte
	//go:embed fonts/NotoSans-Medium.ttf
	fontMedium []byte
	//go:embed fonts/NotoSans-Bold.ttf
	fontBold []byte
)

func rgb(hex uint32) color.RGBA {
	return color.RGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 255}
}

func alpha(c color.RGBA, a uint8) color.RGBA {
	c.A = a
	return c
}

// Palette: dark slate surfaces, coral for "blocked", mint for "free",
// violet for primary actions.
var (
	colBg       = rgb(0x0d0f13)
	colSidebar  = rgb(0x12151b)
	colSurface  = rgb(0x181c24)
	colSurface2 = rgb(0x1f242e)
	colSurface3 = rgb(0x272d39)
	colBorder   = rgb(0x262c37)
	colText     = rgb(0xe8ebf1)
	colMuted    = rgb(0x8a93a6)
	colFaint    = rgb(0x596173)
	colAccent   = rgb(0x8b7cff)
	colAccentHi = rgb(0xa397ff)
	colBlock    = rgb(0xff5d6c)
	colOK       = rgb(0x3dd68c)
	colWarn     = rgb(0xffb447)
)

var categoryColors = map[string]color.RGBA{
	"Redes sociais": rgb(0xff6b9a),
	"Vídeo":         rgb(0xff8a4c),
	"Chat":          rgb(0x5cc8ff),
	"Games":         rgb(0x8bd450),
	"Notícias":      rgb(0xf2c94c),
	"Sistema":       rgb(0x9aa3b5),
}

func categoryColor(cat string) color.RGBA {
	if c, ok := categoryColors[cat]; ok {
		return c
	}
	return colAccent
}

type theme struct {
	medium, bold *g.FontInfo
}

func newTheme() *theme { return &theme{} }

// install registers the fonts; call before MasterWindow.Run.
func (t *theme) install() {
	g.Context.FontAtlas.SetDefaultFontFromBytes(fontRegular)
	g.Context.FontAtlas.SetDefaultFontSize(15)
	t.medium = g.Context.FontAtlas.AddFontFromBytes("NotoSans-Medium", fontMedium)
	t.bold = g.Context.FontAtlas.AddFontFromBytes("NotoSans-Bold", fontBold)
}

// windowStyle is pushed for the whole window every frame.
func windowStyle() *g.StyleSetter {
	return g.Style().
		SetColor(g.StyleColorWindowBg, colBg).
		SetColor(g.StyleColorChildBg, color.RGBA{}).
		SetColor(g.StyleColorPopupBg, colSurface).
		SetColor(g.StyleColorText, colText).
		SetColor(g.StyleColorTextDisabled, colFaint).
		SetColor(g.StyleColorBorder, colBorder).
		SetColor(g.StyleColorFrameBg, colSurface2).
		SetColor(g.StyleColorFrameBgHovered, colSurface3).
		SetColor(g.StyleColorFrameBgActive, colSurface3).
		SetColor(g.StyleColorButton, colSurface2).
		SetColor(g.StyleColorButtonHovered, colSurface3).
		SetColor(g.StyleColorButtonActive, colBorder).
		SetColor(g.StyleColorCheckMark, colText).
		SetColor(g.StyleColorHeader, colSurface2).
		SetColor(g.StyleColorHeaderHovered, colSurface3).
		SetColor(g.StyleColorHeaderActive, colSurface3).
		SetColor(g.StyleColorScrollbarBg, color.RGBA{}).
		SetColor(g.StyleColorScrollbarGrab, colSurface3).
		SetColor(g.StyleColorScrollbarGrabHovered, colFaint).
		SetColor(g.StyleColorScrollbarGrabActive, colMuted).
		SetColor(g.StyleColorSeparator, colBorder).
		SetColor(g.StyleColorTextSelectedBg, alpha(colAccent, 90)).
		SetColor(g.StyleColorModalWindowDimBg, color.RGBA{0, 0, 0, 150}).
		SetStyle(g.StyleVarWindowPadding, 0, 0).
		SetStyle(g.StyleVarFramePadding, 12, 8).
		SetStyle(g.StyleVarItemSpacing, 10, 10).
		SetStyle(g.StyleVarItemInnerSpacing, 8, 6).
		SetStyleFloat(g.StyleVarWindowBorderSize, 0).
		SetStyleFloat(g.StyleVarChildBorderSize, 0).
		SetStyleFloat(g.StyleVarFrameRounding, 8).
		SetStyleFloat(g.StyleVarChildRounding, 12).
		SetStyleFloat(g.StyleVarPopupRounding, 14).
		SetStyleFloat(g.StyleVarWindowRounding, 14).
		SetStyleFloat(g.StyleVarPopupBorderSize, 1).
		SetStyleFloat(g.StyleVarScrollbarSize, 10).
		SetStyleFloat(g.StyleVarScrollbarRounding, 8).
		SetStyleFloat(g.StyleVarGrabRounding, 8)
}

func u32(c color.RGBA) uint32 { return imgui.ColorU32Vec4(g.ToVec4Color(c)) }

func vec(x, y float32) imgui.Vec2 { return imgui.Vec2{X: x, Y: y} }

func gvec(c color.RGBA) imgui.Vec4 { return g.ToVec4Color(c) }
