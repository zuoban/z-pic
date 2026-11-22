package main

import (
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
		ruleIndex: 1,
		rule:      extraRule,
		ready:     true,
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
