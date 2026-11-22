package main

import (
	"math"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

func settingsFontSize(c *ui.Context, size float32) float32 {
	scale := c.Preferences().TextScale
	if scale <= 0 {
		scale = 1
	}
	return size * scale
}

func settingsControlHeight(c *ui.Context, height float32) float32 {
	extra := max(0, settingsFontSize(c, 13)-13) * 1.5
	return height + float32(math.Ceil(float64(extra/4)))*4
}

func settingsPagePadding(c *ui.Context) float32 {
	width, _ := c.Size()
	if width < 560 {
		return 16
	}
	return 24
}

type settingsPanelGlass struct{ material glass.Glass }

func (g settingsPanelGlass) NativeGlass() (ui.NativeGlassSpec, bool) {
	return g.material.NativeGlass()
}

func (g settingsPanelGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	if !p.NativeGlassSupported() {
		// The shader cannot sample the desktop's vibrancy. Give its fallback
		// a readable backing when there is no native glass below the frame.
		backing := p.Theme().Background
		if p.Theme().Dark {
			backing = backing.Alpha(0.6)
		}
		p.FillRadii(box, radii, backing)
	}
	g.material.PaintMaterial(p, box, radii)
}

type settingsControlGlass struct {
	material ui.Material
	backing  ui.Color
	border   ui.Color
}

func (g settingsControlGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	// Native panels sit below the frame; the shader sees their transparent
	// holes. A neutral backing keeps small controls from sampling black.
	p.FillRadii(box, radii, g.backing)
	g.material.PaintMaterial(p, box, radii)
	if g.border.A > 0 {
		p.Stroke(box, g.border, radii[0], 1)
	}
}

// The settings panels sample the native window material; fields stay inset and
// opaque enough to read. Accessibility preferences keep the ordinary surfaces.
func settingsPanel(c *ui.Context, key string) ui.Element {
	panel := ui.Column(c.Key(key)).FillWidth().Padding(14, 16).Gap(10).Radius(panelRadius(c)).
		Background(panelSurface(c)).Border(1, glassBorder(c))
	if usesGlass(c) {
		tint := ui.RGBA(255, 255, 255, 0.62)
		if c.Theme().Dark {
			tint = ui.RGBA(22, 25, 32, 0.62)
		}
		panel.Background(ui.Transparent).Border(0, ui.Transparent).
			Material(settingsPanelGlass{material: glass.Glass{Native: true, Tint: tint}})
	} else {
		cardElevation(panel, c)
	}
	return panel
}

func settingsGlassControl(el ui.Element, c *ui.Context, height float32, disabled bool, tint ui.Color) ui.Element {
	height = settingsControlHeight(c, height)
	if usesGlass(c) && !disabled {
		material := glass.Glass{Tint: tint, Interactive: !c.Preferences().ReduceMotion}.BuildMaterial(el)
		border := controlBorder(c)
		if el.Pressed() || el.Hovered() {
			border = controlHoverBorder(c)
		}
		if tint.A >= 230 {
			border = ui.Transparent
		} else if tint.A > 0 {
			border = selectionBorder(c)
		}
		el.Radius(height/2).Background(ui.Transparent).Border(0, ui.Transparent).
			Material(settingsControlGlass{material: material, backing: glassControlSurface(c), border: border})
	}
	return el
}

func settingsSaveButton(c *ui.Context, disabled bool) ui.Element {
	h := settingsControlHeight(c, 36)
	button := primaryAction(c, "save-settings", "保存", disabled).FontSize(settingsFontSize(c, 13)).
		Height(h).Radius(pillRadius(h, c))
	if usesGlass(c) && !disabled {
		settingsGlassControl(button, c, 36, false, c.Theme().AccentHover).
			TextColor(c.Theme().AccentText)
	}
	return button
}

func settingsButton(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	button := quietButton(c, key, label, settingsControlHeight(c, height), disabled).FontSize(settingsFontSize(c, 13))
	return settingsGlassControl(button, c, height, disabled, ui.Transparent)
}
