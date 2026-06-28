// Elemental is a match-three game where every piece is an "energy cell" rendered
// entirely by a procedural GLSL fragment shader - swirling plasma, electric
// arcs, liquid metal, lava, crystal, nebula and hologram. There is no sprite
// art: the shader is the sprite. It is built to show off Fyne's canvas.Shader
// object type and the power of OpenGL inside a Fyne app.
package main

import (
	_ "embed"
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

//go:embed Icon.png
var iconBytes []byte

func main() {
	// numTypes is hard-coded in board.go; keep it honest against the material set.
	if numTypes != len(materials) {
		panic("numTypes does not match the number of materials")
	}

	a := app.NewWithID("com.fyshos.elemental")
	a.Settings().SetTheme(elementalTheme{})
	a.SetIcon(fyne.NewStaticResource("Icon.png", iconBytes))
	w := a.NewWindow("Elemental")
	w.SetPadded(false)

	score := canvas.NewText("0", color.NRGBA{R: 120, G: 220, B: 255, A: 255})
	score.TextSize = 28
	score.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	title := canvas.NewText("E L E M E N T A L", color.NRGBA{R: 200, G: 180, B: 255, A: 255})
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	game := NewGame(func(s int) {
		// onScore is called from the animation goroutine; marshal to the UI thread.
		fyne.Do(func() {
			score.Text = strconv.Itoa(s)
			score.Refresh()
		})
	})

	scoreLabel := canvas.NewText("SCORE", color.NRGBA{R: 130, G: 140, B: 180, A: 255})
	scoreLabel.TextSize = 14
	scoreLabel.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	scoreBox := container.NewHBox(scoreLabel, layoutSpacer(8), score)

	// Animated hexagon logo that cycles through every element's hue.
	logo := newLogoShader()
	logoBox := container.NewGridWrap(fyne.NewSize(40, 40), logo)
	titleRow := container.NewHBox(logoBox, layoutSpacer(10), title)

	// Header: logo, title and score over a violet console bar.
	headerPanel := newPanelShader(0.45, 0.30, 0.85)
	header := container.NewStack(
		headerPanel,
		container.NewPadded(container.NewBorder(nil, nil, titleRow, scoreBox)),
	)

	// Footer: how-to-play hint over a cyan console bar. A wrapping label lets the
	// text reflow onto a second line ("match 3+ of the same element") when the
	// window is too narrow to fit it on one.
	footerPanel := newPanelShader(0.20, 0.55, 0.85)
	hint := widget.NewLabelWithStyle(
		"Tap a cell then a neighbour to swap  -  or drag  -  match 3+ of the same element",
		fyne.TextAlignCenter, fyne.TextStyle{})
	hint.Wrapping = fyne.TextWrapWord
	footer := container.NewStack(footerPanel, container.NewPadded(hint))

	content := container.NewBorder(header, footer, nil, nil, game)
	w.SetContent(content)

	// Bring the HUD panels and logo to life with their own shader animation.
	canvas.NewShaderAnimation(headerPanel).Start()
	canvas.NewShaderAnimation(footerPanel).Start()
	canvas.NewShaderAnimation(logo).Start()

	w.Resize(fyne.NewSize(486, 590))
	w.ShowAndRun()
}

// layoutSpacer is a fixed-width transparent gap for inline spacing.
func layoutSpacer(w float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 0))
	return r
}
