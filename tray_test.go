package main

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestTrayClipboardCompressDisabledMatchesToolbar(t *testing.T) {
	app := newApplication()
	app.readClipboardPNG = func() ([]byte, int64, error) { return nil, 0, nil }
	app.replaceClipboardPNG = func([]byte, int64) error { return nil }

	check := func(label string, wantDisabled bool) {
		t.Helper()
		menu := app.buildTrayMenu()
		item := menu.ItemByID("tray-compress-clipboard")
		if item == nil {
			t.Fatalf("%s: expected tray-compress-clipboard item", label)
		}
		if item.Disabled != wantDisabled {
			t.Fatalf("%s: Disabled = %v, want %v (canCompress=%v)", label, item.Disabled, wantDisabled, app.canCompressClipboardPNG())
		}
		if item.Disabled != !app.canCompressClipboardPNG() {
			t.Fatalf("%s: tray disabled (%v) diverged from !canCompressClipboardPNG() (%v)", label, item.Disabled, !app.canCompressClipboardPNG())
		}
	}

	check("idle", false)

	app.busy = true
	check("busy", true)
	app.busy = false

	app.dropImporting = true
	check("dropImporting", true)
	app.dropImporting = false

	app.dropScanning = true
	check("dropScanning", true)
	app.dropScanning = false

	app.settingsOpen = true
	check("settingsOpen", true)
	app.settingsOpen = false

	app.dropQueue = []transfer.Data{{}}
	check("dropQueue non-empty", true)
	app.dropQueue = nil

	check("idle again", false)
}

func TestTraySyncOnFloatingToggleAndMainViewNoThrashing(t *testing.T) {
	app := testComparisonApplication()
	app.tray = &appTray{}
	refreshes := 0
	app.onTrayRefresh = func() { refreshes++ }

	// Opening and closing floating panel triggers refreshTrayMenu only when floatingOpen changes.
	app.setFloatingOpen(true)
	if refreshes != 1 || !app.tray.lastSynced.floatingOpen {
		t.Fatalf("setFloatingOpen(true): refreshes=%d syncedFloating=%v", refreshes, app.tray.lastSynced.floatingOpen)
	}
	app.setFloatingOpen(true)
	if refreshes != 1 {
		t.Fatalf("redundant setFloatingOpen(true) must not refresh tray: got %d", refreshes)
	}
	app.setFloatingOpen(false)
	if refreshes != 2 || app.tray.lastSynced.floatingOpen {
		t.Fatalf("setFloatingOpen(false): refreshes=%d syncedFloating=%v", refreshes, app.tray.lastSynced.floatingOpen)
	}
	app.setFloatingOpen(false)
	if refreshes != 2 {
		t.Fatalf("redundant setFloatingOpen(false) must not refresh tray: got %d", refreshes)
	}

	// Steady main-view renders and pointer moves must not thrash tray refreshes.
	tester := ui.NewTester(app.view, 880, 660)
	baseRefreshes := refreshes
	for range 5 {
		tester.Frame()
	}
	tester.Move(200, 200)
	tester.Move(320, 260)
	if refreshes != baseRefreshes {
		t.Fatalf("steady main-window renders/hovers must not refresh tray: got %d, want %d", refreshes, baseRefreshes)
	}

	// Each tray-relevant state change refreshes once, and repeated frames stay idle.
	transitions := []struct {
		name  string
		apply func()
	}{
		{"busy=true", func() { app.busy = true }},
		{"clipboardImporting=true", func() { app.clipboardImporting = true }},
		{"dropImporting=true", func() { app.dropImporting = true }},
		{"dropScanning=true", func() { app.dropScanning = true }},
		{"reset all", func() {
			app.busy = false
			app.clipboardImporting = false
			app.dropImporting = false
			app.dropScanning = false
		}},
	}
	for _, tr := range transitions {
		want := refreshes + 1
		tr.apply()
		tester.Frame()
		if refreshes != want {
			t.Fatalf("%s: expected 1 tray refresh (total %d), got %d", tr.name, want, refreshes)
		}
		tester.Frame()
		if refreshes != want {
			t.Fatalf("%s follow-up frame must not re-refresh tray: got %d, want %d", tr.name, refreshes, want)
		}
	}
}

func TestTraySyncOnAsyncUpdate(t *testing.T) {
	app := newApplication()
	app.tray = &appTray{}
	refreshes := 0
	app.onTrayRefresh = func() { refreshes++ }
	app.dispatchUpdate = func(fn func()) { fn() }

	app.refreshTrayMenu()
	if refreshes != 1 {
		t.Fatalf("initial refreshTrayMenu: got %d, want 1", refreshes)
	}

	// Async update without tray-relevant changes must not refresh.
	app.update(func() {
		app.message = "unrelated"
	})
	if refreshes != 1 {
		t.Fatalf("unrelated update must not refresh tray: got %d", refreshes)
	}

	// Async update changing busy state must refresh once.
	app.update(func() {
		app.busy = true
	})
	if refreshes != 2 {
		t.Fatalf("busy transition in update must refresh tray once: got %d", refreshes)
	}
}

func TestMainViewShowsWatchStatusBarForExtraRuleOnly(t *testing.T) {
	app := newApplication()
	extraRule := folderWatchRule{
		InputDir:  "/tmp/watch-in-2",
		OutputDir: "/tmp/watch-out-2",
		Preset:    "均衡",
	}
	app.watch.extraRules = []folderWatchRule{extraRule}
	app.setWatchSessionForRule(1, &desktopWatchSession{
		rule:  extraRule,
		ready: true,
	})

	if app.watch.session != nil || app.watch.last.Input != "" || app.watch.err != nil {
		t.Fatal("test precondition violated: primary session/last/err must be empty")
	}

	tester := ui.NewTester(app.view, 900, 660)
	if !tester.HasText("正在监听") {
		t.Fatal("expected watch status bar to be visible when only an extra watch rule is running")
	}
	if !tester.HasText("停止监听") {
		t.Fatal("expected stop watch button to be visible when only an extra watch rule is running")
	}
}

func TestTrayCheckUpdatesGuardsBusyAndConfirmDelete(t *testing.T) {
	t.Run("busy main window skips background update check when settings cannot open", func(t *testing.T) {
		app := newApplication()
		app.busy = true
		menu := app.buildTrayMenu()
		item := menu.ItemByID("tray-check-updates")
		if item == nil || item.Click == nil {
			t.Fatal("expected tray-check-updates item with click handler")
		}
		item.Click(item, nil)
		if app.settingsOpen || app.editor != nil {
			t.Fatal("settings must remain closed while app is busy")
		}
		if app.updateState != updateIdle {
			t.Fatalf("expected updateState to stay updateIdle when settings cannot open, got %v", app.updateState)
		}
	})

	t.Run("switching from preset delete confirmation clears confirmDelete", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		app := testSelectedApplication()
		app.updateChecker = updateCheckerConfig{
			endpoint:       srv.URL,
			currentVersion: "0.1.0",
			client:         srv.Client(),
		}
		app.dispatchUpdate = func(fn func()) {}
		app.setSettingsOpen(true)
		app.editor.section = settingsPresets
		app.editor.confirmDelete = true

		menu := app.buildTrayMenu()
		item := menu.ItemByID("tray-check-updates")
		if item == nil || item.Click == nil {
			t.Fatal("expected tray-check-updates item with click handler")
		}
		item.Click(item, nil)
		defer app.cancelUpdateCheck()

		if app.editor.section != settingsAbout {
			t.Fatalf("expected section=settingsAbout, got %v", app.editor.section)
		}
		if app.editor.confirmDelete {
			t.Fatal("confirmDelete must be cleared when switching to about section from tray")
		}
		if app.settingsDraftLocked() {
			t.Fatal("about settings must not remain locked after tray check-updates")
		}
		if app.updateState != updateChecking {
			t.Fatalf("expected updateState=updateChecking, got %v", app.updateState)
		}
	})
}

func TestTrayTemplateIconMonochromeAntialiased(t *testing.T) {
	data := buildTrayTemplateIconPNG()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode(buildTrayTemplateIconPNG()): %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 32 || bounds.Dy() != 32 {
		t.Fatalf("expected 32x32 tray icon, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	var opaqueCount, aaCount, transparentCount int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			px := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			switch {
			case px.A == 0:
				transparentCount++
			case px.A == 255:
				opaqueCount++
			default:
				aaCount++
			}
			if px.A > 0 && (px.R != 0 || px.G != 0 || px.B != 0) {
				t.Fatalf("non-transparent pixel (%d,%d) must be pure black monochrome, got rgba(%d,%d,%d,%d)",
					x, y, px.R, px.G, px.B, px.A)
			}
		}
	}

	if opaqueCount < 80 {
		t.Fatalf("expected crisp opaque icon strokes, got only %d opaque pixels", opaqueCount)
	}
	if aaCount < 40 {
		t.Fatalf("expected smooth anti-aliased edge pixels (0 < A < 255), got only %d", aaCount)
	}
	if transparentCount < 400 {
		t.Fatalf("expected transparent background/interior negative space, got only %d transparent pixels", transparentCount)
	}

	// Outer corners and perimeter inset must remain fully transparent.
	for _, pt := range [][2]int{
		{0, 0}, {1, 0}, {0, 1}, {1, 1},
		{31, 0}, {30, 0}, {31, 1}, {30, 1},
		{0, 31}, {1, 31}, {0, 30}, {1, 30},
		{31, 31}, {30, 31}, {31, 30}, {30, 30},
		{16, 0}, {16, 31}, {0, 16}, {31, 16},
	} {
		px := color.NRGBAModel.Convert(img.At(pt[0], pt[1])).(color.NRGBA)
		if px.A != 0 {
			t.Fatalf("outer background/corner pixel (%d,%d) must be transparent, got A=%d", pt[0], pt[1], px.A)
		}
	}

	// Interior breathing space between frame and brackets should stay clear,
	// while the center compression accent is solid.
	for _, pt := range [][2]int{{7, 7}, {24, 24}, {24, 7}, {7, 24}} {
		px := color.NRGBAModel.Convert(img.At(pt[0], pt[1])).(color.NRGBA)
		if px.A != 0 {
			t.Fatalf("interior negative space (%d,%d) should be transparent, got A=%d", pt[0], pt[1], px.A)
		}
	}
	center := color.NRGBAModel.Convert(img.At(16, 16)).(color.NRGBA)
	if center.A != 255 {
		t.Fatalf("expected solid center compression accent at (16,16), got A=%d", center.A)
	}
}
