package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"path/filepath"

	"github.com/egoist/mygo"
)

type traySyncSnapshot struct {
	mainVisible          bool
	floatingOpen         bool
	busy                 bool
	clipboardImporting   bool
	dropImporting        bool
	dropScanning         bool
	clipboardAvailable   bool
	canCompressClipboard bool
	scheme               string
	activeWatchCount     int
	watchRulesHash       uint64
}

type appTray struct {
	tray       *mygo.Tray
	lastSynced traySyncSnapshot
	synced     bool
}

func sdRoundedRect(x, y, cx, cy, hx, hy, radius float64) float64 {
	qx := math.Abs(x-cx) - (hx - radius)
	qy := math.Abs(y-cy) - (hy - radius)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - radius
}

func sdSegment(px, py, ax, ay, bx, by float64) float64 {
	pax, pay := px-ax, py-ay
	bax, bay := bx-ax, by-ay
	denom := bax*bax + bay*bay
	if denom == 0 {
		return math.Hypot(pax, pay)
	}
	t := math.Max(0, math.Min(1, (pax*bax+pay*bay)/denom))
	return math.Hypot(pax-bax*t, pay-bay*t)
}

func trayIconSDF(x, y float64) float64 {
	const (
		cx, cy       = 16.0, 16.0
		halfStroke   = 1.1
		frameHalf    = 11.9
		frameRadius  = 5.4
		invSqrt2     = math.Sqrt2 / 2.0
		diamondHalf  = 1.75
		diamondRound = 0.5
	)
	// Outer rounded frame (~3px inset, ~2.2px stroke, ~6px outer corner radius).
	d := math.Abs(sdRoundedRect(x, y, cx, cy, frameHalf, frameHalf, frameRadius)) - halfStroke

	// Signature opposing viewfinder corner brackets unified with brandLockup.
	tlHoriz := sdSegment(x, y, 9.5, 9.5, 14.0, 9.5) - halfStroke
	tlVert := sdSegment(x, y, 9.5, 9.5, 9.5, 14.0) - halfStroke
	brHoriz := sdSegment(x, y, 18.0, 22.5, 22.5, 22.5) - halfStroke
	brVert := sdSegment(x, y, 22.5, 18.0, 22.5, 22.5) - halfStroke
	d = math.Min(d, math.Min(math.Min(tlHoriz, tlVert), math.Min(brHoriz, brVert)))

	// Subtle center compression diamond accent.
	dx, dy := x-cx, y-cy
	u := (dx + dy) * invSqrt2
	v := (dx - dy) * invSqrt2
	diamond := sdRoundedRect(u, v, 0, 0, diamondHalf, diamondHalf, diamondRound)
	return math.Min(d, diamond)
}

// buildTrayTemplateIconPNG generates a crisp 32x32 Retina monochrome template
// icon (black strokes on transparent background) using 4x supersampled (128x128
// box-filtered to 32x32) analytic signed distance fields.
func buildTrayTemplateIconPNG() []byte {
	const (
		size        = 32
		supersample = 4
		subSamples  = supersample * supersample
		subInv      = 1.0 / float64(supersample)
	)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := range size {
		for px := range size {
			var coverage float64
			for sy := range supersample {
				y := float64(py) + (float64(sy)+0.5)*subInv
				for sx := range supersample {
					x := float64(px) + (float64(sx)+0.5)*subInv
					d := trayIconSDF(x, y)
					subCov := 0.5 - d*float64(supersample)
					if subCov > 0 {
						if subCov >= 1 {
							coverage += 1
						} else {
							coverage += subCov
						}
					}
				}
			}
			alpha := uint8(math.Round((coverage / float64(subSamples)) * 255.0))
			if alpha > 0 {
				img.SetNRGBA(px, py, color.NRGBA{R: 0, G: 0, B: 0, A: alpha})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func (s *application) trayToolTip() string {
	active := s.activeFolderWatchCount()
	if active > 0 {
		return fmt.Sprintf("Z Pic · 正在监听 %d 个文件夹", active)
	}
	return "Z Pic · 图片压缩"
}

func (s *application) traySyncSnapshot() traySyncSnapshot {
	return traySyncSnapshot{
		mainVisible:          s.window != nil && s.window.IsVisible(),
		floatingOpen:         s.floatingOpen,
		busy:                 s.busy,
		clipboardImporting:   s.clipboardImporting,
		dropImporting:        s.dropImporting,
		dropScanning:         s.dropScanning,
		clipboardAvailable:   s.clipboardPNGCompressionAvailable(),
		canCompressClipboard: s.canCompressClipboardPNG(),
		scheme:               s.scheme,
		activeWatchCount:     s.activeFolderWatchCount(),
		watchRulesHash:       s.trayWatchRulesHash(),
	}
}

func (s *application) trayWatchRulesHash() uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	mixByte := func(b byte) {
		h ^= uint64(b)
		h *= prime64
	}
	mixString := func(str string) {
		for i := range len(str) {
			mixByte(str[i])
		}
		mixByte(0)
	}
	mixUint64 := func(value uint64) {
		for range 8 {
			mixByte(byte(value))
			value >>= 8
		}
	}
	mixRule := func(ruleID uint64, rule folderWatchRule) {
		mixUint64(ruleID)
		mixString(rule.InputDir)
		mixString(rule.OutputDir)
		mixString(rule.Preset)
		sess := s.watchSessionForRuleID(ruleID)
		var flags byte
		if sess != nil {
			flags |= 1
			if sess.stopping {
				flags |= 2
			}
		}
		mixByte(flags)
	}
	for _, item := range s.watchRulesWithIDs() {
		mixRule(item.id, item.rule)
	}
	return h
}

func (s *application) setupTray() {
	if s.tray != nil || !mygo.App.IsReady() {
		return
	}
	snapshot := s.traySyncSnapshot()
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "z-pic-menubar",
		Icon:           buildTrayTemplateIconPNG(),
		IconIsTemplate: true,
		ToolTip:        s.trayToolTip(),
		Menu:           s.buildTrayMenu(),
	})
	if err != nil {
		return
	}
	s.tray = &appTray{
		tray:       tray,
		lastSynced: snapshot,
		synced:     true,
	}
}

func (s *application) closeTray() {
	if s.tray == nil {
		return
	}
	if s.tray.tray != nil {
		s.tray.tray.Destroy()
	}
	s.tray = nil
}

func (s *application) applyTrayRefresh(snapshot traySyncSnapshot) {
	s.tray.lastSynced = snapshot
	s.tray.synced = true
	if s.tray.tray != nil {
		s.tray.tray.SetMenu(s.buildTrayMenu())
		s.tray.tray.SetToolTip(s.trayToolTip())
	}
	if s.onTrayRefresh != nil {
		s.onTrayRefresh()
	}
}

func (s *application) refreshTrayMenu() {
	if s.tray == nil || s.closed {
		return
	}
	s.applyTrayRefresh(s.traySyncSnapshot())
}

func (s *application) syncTrayMenuIfChanged() {
	if s.tray == nil || s.closed {
		return
	}
	snapshot := s.traySyncSnapshot()
	if s.tray.synced && snapshot == s.tray.lastSynced {
		return
	}
	s.applyTrayRefresh(snapshot)
}

func (s *application) shouldHideMainWindowOnClose() bool {
	return !s.quitting && s.keepInMenuBar && s.tray != nil
}

func (s *application) buildTrayMenu() *mygo.Menu {
	items := make([]*mygo.MenuItem, 0, 12)

	mainVisible := s.window != nil && s.window.IsVisible()
	windowLabel := "显示主窗口"
	if mainVisible {
		windowLabel = "隐藏主窗口"
	}
	items = append(items, &mygo.MenuItem{
		ID:    "tray-toggle-main",
		Label: windowLabel,
		Click: func(*mygo.MenuItem, *mygo.Window) {
			if s.window == nil {
				return
			}
			if s.window.IsVisible() {
				s.window.Hide()
			} else {
				s.showMainWindow()
			}
			s.refreshTrayMenu()
		},
	})

	items = append(items, &mygo.MenuItem{
		ID:      "tray-toggle-floating",
		Label:   "悬浮压缩窗",
		Type:    mygo.MenuItemCheckbox,
		Checked: s.floatingOpen,
		Click: func(*mygo.MenuItem, *mygo.Window) {
			s.setFloatingOpen(!s.floatingOpen)
			s.syncTrayMenuIfChanged()
		},
	})

	if s.clipboardPNGCompressionAvailable() {
		items = append(items, &mygo.MenuItem{
			ID:       "tray-compress-clipboard",
			Label:    "压缩剪贴板图片",
			Disabled: !s.canCompressClipboardPNG(),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				s.compressClipboardPNG()
				s.syncTrayMenuIfChanged()
			},
		})
	}

	items = append(items, mygo.Separator())

	activeCount := s.activeFolderWatchCount()
	rules := s.watchRulesWithIDs()
	configured := 0
	for _, item := range rules {
		if item.rule.InputDir != "" && item.rule.OutputDir != "" {
			configured++
		}
	}
	watchHeader := "文件夹监听 · 未运行"
	if activeCount > 0 {
		watchHeader = fmt.Sprintf("文件夹监听 · 运行中（%d）", activeCount)
	}
	items = append(items, &mygo.MenuItem{
		ID:       "tray-watch-header",
		Label:    watchHeader,
		Disabled: true,
	})

	for _, item := range rules {
		rule := item.rule
		if rule.InputDir == "" || rule.OutputDir == "" {
			continue
		}
		ruleID := item.id
		ruleCopy := rule
		session := s.watchSessionForRuleID(ruleID)
		running := session != nil
		name := filepath.Base(rule.InputDir)
		if name == "" || name == "." || name == "/" {
			name = rule.InputDir
		}
		presetName := rule.Preset
		if presetName == "" {
			presetName = s.scheme
		}
		label := fmt.Sprintf("%s（%s）", name, presetName)
		items = append(items, &mygo.MenuItem{
			ID:       fmt.Sprintf("tray-watch-rule-%d", ruleID),
			Label:    label,
			Type:     mygo.MenuItemCheckbox,
			Checked:  running,
			Disabled: session != nil && session.stopping,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if s.watchSessionForRuleID(ruleID) != nil {
					s.stopWatchRuleByID(ruleID)
				} else {
					if err := s.startWatchRuleByID(ruleID, ruleCopy, s.watch.settings.Notify, true); err != nil {
						s.watch.err = err
						s.invalidateFolderWatchViews()
					}
				}
				s.refreshTrayMenu()
			},
		})
	}

	if configured > 1 {
		if activeCount < configured {
			items = append(items, &mygo.MenuItem{
				ID:    "tray-watch-start-all",
				Label: "启动全部监听",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					s.startAllFolderWatches()
					s.refreshTrayMenu()
				},
			})
		}
		if activeCount > 0 {
			items = append(items, &mygo.MenuItem{
				ID:    "tray-watch-stop-all",
				Label: "停止全部监听",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					s.stopAllFolderWatches()
					s.refreshTrayMenu()
				},
			})
		}
	}

	items = append(items, mygo.Separator())
	items = append(items, &mygo.MenuItem{
		ID:    "tray-check-updates",
		Label: "检查更新…",
		Click: func(*mygo.MenuItem, *mygo.Window) {
			s.showMainWindow()
			s.setSettingsOpen(true)
			if s.settingsOpen && s.editor != nil {
				s.editor.confirmDelete = false
				s.editor.section = settingsAbout
				if s.settingsWindow != nil {
					s.settingsWindow.Invalidate()
				}
				s.checkAppUpdates()
			}
			s.refreshTrayMenu()
		},
	})
	items = append(items, &mygo.MenuItem{
		ID:    "tray-settings",
		Label: "设置…",
		Click: func(*mygo.MenuItem, *mygo.Window) {
			s.showMainWindow()
			s.setSettingsOpen(true)
			s.refreshTrayMenu()
		},
	})
	items = append(items, &mygo.MenuItem{
		ID:    "tray-quit",
		Label: "退出 Z Pic",
		Click: func(*mygo.MenuItem, *mygo.Window) {
			s.quitting = true
			mygo.App.Quit()
		},
	})
	return mygo.NewMenu(items)
}
