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
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed Icon.png
var iconBytes []byte

// prefKeyMute remembers whether the player has muted the sound.
const prefKeyMute = "sound.mute"

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

	// Procedural sound for key events, with the muted choice restored from prefs.
	sound := newSoundPlayer()
	sound.SetMuted(a.Preferences().Bool(prefKeyMute))
	game.onSound = sound.Play

	// A mute toggle whose icon confirms the current state.
	var muteBtn *widget.Button
	muteIcon := func() fyne.Resource {
		if sound.Muted() {
			return theme.VolumeMuteIcon()
		}
		return theme.VolumeUpIcon()
	}
	muteBtn = widget.NewButtonWithIcon("", muteIcon(), func() {
		muted := !sound.Muted()
		sound.SetMuted(muted)
		a.Preferences().SetBool(prefKeyMute, muted)
		muteBtn.SetIcon(muteIcon())
	})
	muteBtn.Importance = widget.LowImportance

	scoreLabel := canvas.NewText("SCORE", color.NRGBA{R: 130, G: 140, B: 180, A: 255})
	scoreLabel.TextSize = 14
	scoreLabel.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	scoreBox := container.NewHBox(scoreLabel, layoutSpacer(8), score)

	// Animated hexagon logo that cycles through every element's hue.
	logo := newLogoShader()
	logoBox := container.NewGridWrap(fyne.NewSize(40, 40), logo)
	titleRow := container.NewHBox(logoBox, layoutSpacer(10), title)

	// Core-energy gauge and difficulty level make up the jeopardy row. The game
	// drives the gauge's uniforms each frame, so it needs no separate animation.
	levelText := canvas.NewText("LV 0", color.NRGBA{R: 150, G: 200, B: 255, A: 255})
	levelText.TextSize = 13
	levelText.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	energyBar := container.NewStack(game.EnergyBar(), fixedSpacer(0, 16))
	energyRow := container.NewBorder(nil, nil, levelText, nil, energyBar)

	rightSide := container.NewHBox(scoreBox, layoutSpacer(8), muteBtn)
	topRow := container.NewBorder(nil, nil, titleRow, rightSide)

	// Header: logo/title/score above the core gauge, over a violet console bar.
	headerPanel := newPanelShader(0.45, 0.30, 0.85)
	header := container.NewStack(
		headerPanel,
		container.NewPadded(container.NewVBox(topRow, energyRow)),
	)

	// Footer: how-to-play hint over a cyan console bar. A wrapping label lets the
	// text reflow onto a second line ("match 3+ of the same element") when the
	// window is too narrow to fit it on one.
	footerPanel := newPanelShader(0.20, 0.55, 0.85)
	hint := widget.NewLabelWithStyle(
		"Tap a cell then a neighbour to swap  -  or drag  -  every 3+ match recharges the core",
		fyne.TextAlignCenter, fyne.TextStyle{})
	hint.Wrapping = fyne.TextWrapWord
	footer := container.NewStack(footerPanel, container.NewPadded(hint))

	content := container.NewBorder(header, footer, nil, nil, game)
	w.SetContent(content)

	// Difficulty level readout, plus a brief screen flash on each new level so the
	// step up in pace is unmissable (the game pauses itself behind it).
	game.onLevel = func(l int) {
		fyne.Do(func() {
			levelText.Text = "LV " + strconv.Itoa(l)
			levelText.Refresh()
			if l > 0 {
				showLevelFlash(w.Canvas(), l)
			}
		})
	}

	// Game over: the core has gone dark. Offer a fresh start.
	var overPopup *widget.PopUp
	game.onGameOver = func(s int) {
		fyne.Do(func() {
			overPopup = newGameOverPopup(w.Canvas(), s, func() {
				overPopup.Hide()
				game.Restart()
			})
			overPopup.Show()
		})
	}

	// Bring the HUD panels and logo to life with their own shader animation.
	canvas.NewShaderAnimation(headerPanel).Start()
	canvas.NewShaderAnimation(footerPanel).Start()
	canvas.NewShaderAnimation(logo).Start()

	w.Resize(fyne.NewSize(486, 620))
	w.ShowAndRun()
}

// newGameOverPopup builds the "core dark" modal with the final score and a
// restart button.
func newGameOverPopup(c fyne.Canvas, score int, onRestart func()) *widget.PopUp {
	heading := canvas.NewText("CORE DARK", color.NRGBA{R: 255, G: 80, B: 80, A: 255})
	heading.TextSize = 32
	heading.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	heading.Alignment = fyne.TextAlignCenter

	sub := canvas.NewText("the core ran out of energy", color.NRGBA{R: 200, G: 185, B: 215, A: 255})
	sub.TextSize = 14
	sub.TextStyle = fyne.TextStyle{Monospace: true}
	sub.Alignment = fyne.TextAlignCenter

	final := canvas.NewText("SCORE  "+strconv.Itoa(score), color.NRGBA{R: 120, G: 220, B: 255, A: 255})
	final.TextSize = 22
	final.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	final.Alignment = fyne.TextAlignCenter

	btn := widget.NewButton("New Core", onRestart)
	btn.Importance = widget.HighImportance

	box := container.NewVBox(
		heading,
		sub,
		fixedSpacer(0, 8),
		final,
		fixedSpacer(0, 8),
		btn,
	)
	return widget.NewModalPopUp(container.NewPadded(box), c)
}

// showLevelFlash takes over the screen for a beat to announce a new level. It is
// timed to the pause the game takes on level-up: the banner pops, holds over the
// frozen board, then fades and dismisses just as play resumes.
func showLevelFlash(c fyne.Canvas, level int) {
	const headR, headG, headB = 150, 220, 255
	const subR, subG, subB = 210, 195, 230

	heading := canvas.NewText("LEVEL "+strconv.Itoa(level), color.NRGBA{headR, headG, headB, 255})
	heading.TextSize = 52
	heading.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	heading.Alignment = fyne.TextAlignCenter

	sub := canvas.NewText("THE CORE DRAINS FASTER", color.NRGBA{subR, subG, subB, 255})
	sub.TextSize = 13
	sub.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	sub.Alignment = fyne.TextAlignCenter

	panel := newPanelShader(0.30, 0.18, 0.70)
	panelAnim := canvas.NewShaderAnimation(panel)
	panelAnim.Start()

	body := container.NewPadded(container.NewVBox(heading, fixedSpacer(0, 6), sub))
	pop := widget.NewPopUp(container.NewStack(panel, body), c)
	pop.Show()

	// Fade the banner out toward the end of the pause...
	time.AfterFunc(700*time.Millisecond, func() {
		fyne.Do(func() {
			fade := &fyne.Animation{
				Duration: 350 * time.Millisecond,
				Curve:    fyne.AnimationEaseInOut,
				Tick: func(f float32) {
					a := uint8(255 * (1 - f))
					heading.Color = color.NRGBA{headR, headG, headB, a}
					sub.Color = color.NRGBA{subR, subG, subB, a}
					heading.Refresh()
					sub.Refresh()
				},
			}
			fade.Start()
		})
	})
	// ...then dismiss it once faded.
	time.AfterFunc(1100*time.Millisecond, func() {
		fyne.Do(func() {
			panelAnim.Stop()
			pop.Hide()
		})
	})
}

// layoutSpacer is a fixed-width transparent gap for inline spacing.
func layoutSpacer(w float32) fyne.CanvasObject { return fixedSpacer(w, 0) }

// fixedSpacer is a transparent box that forces a minimum size for spacing.
func fixedSpacer(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}
