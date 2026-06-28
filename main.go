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
	a.SetIcon(fyne.NewStaticResource("Icon.png", iconBytes))
	w := a.NewWindow("Elemental")

	score := canvas.NewText("0", color.NRGBA{R: 120, G: 220, B: 255, A: 255})
	score.TextSize = 26
	score.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	title := canvas.NewText("ELEMENTAL", color.NRGBA{R: 200, G: 180, B: 255, A: 255})
	title.TextSize = 22
	title.TextStyle = fyne.TextStyle{Bold: true}

	game := NewGame(func(s int) {
		// onScore is called from the animation goroutine; marshal to the UI thread.
		fyne.Do(func() {
			score.Text = strconv.Itoa(s)
			score.Refresh()
		})
	})

	scoreLabel := canvas.NewText("SCORE", color.NRGBA{R: 130, G: 130, B: 160, A: 255})
	scoreLabel.TextStyle = fyne.TextStyle{Bold: true}
	scoreBox := container.NewHBox(scoreLabel, score)

	bar := container.NewBorder(nil, nil, title, scoreBox)

	hint := widget.NewLabelWithStyle(
		"Tap a cell, then a neighbour to swap - or drag. Match 3+ of the same element.",
		fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	content := container.NewBorder(bar, hint, nil, nil, game)
	w.SetContent(content)
	w.Resize(fyne.NewSize(640, 760))
	w.ShowAndRun()
}
