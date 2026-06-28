package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// elementalTheme gives the whole app a single look: a deep-space dark palette
// with cyan/violet accents and one monospace typeface across every widget, so
// the HUD reads as part of the same sci-fi console as the shader board.
type elementalTheme struct{}

var _ fyne.Theme = elementalTheme{}

func (elementalTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 6, G: 6, B: 14, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 220, G: 226, B: 246, A: 255}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 120, G: 200, B: 255, A: 255}
	case theme.ColorNameHover:
		return color.NRGBA{R: 60, G: 90, B: 150, A: 90}
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return color.NRGBA{R: 16, G: 18, B: 34, A: 255}
	}
	// Everything else follows the standard dark theme.
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

// Font returns a monospace face for every style, unifying the typography.
func (elementalTheme) Font(style fyne.TextStyle) fyne.Resource {
	style.Monospace = true
	style.Italic = false
	return theme.DefaultTheme().Font(style)
}

func (elementalTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (elementalTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}
