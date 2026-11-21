package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDesktopFolderWatchControls(t *testing.T) {
	app := newApplication()
	tester := openTestSettings(t, app)
	if err := tester.Click("文件夹监听"); err != nil {
		t.Fatal(err)
	}
	start, ok := tester.AccessByRole(ui.RoleButton, "开始监听")
	if !ok || start.Offscreen || !start.Disabled {
		t.Fatalf("Start must be visible and disabled until both folders are selected: %+v", start)
	}
	for _, name := range []string{"选择输入目录", "选择输出目录"} {
		button, ok := tester.AccessByRole(ui.RoleButton, name)
		if !ok || button.Offscreen || button.Disabled {
			t.Fatalf("folder picker %q must be reachable: %+v", name, button)
		}
	}

	app.editor.watch = folderWatchSettings{InputDir: t.TempDir(), OutputDir: t.TempDir(), Preset: "≤ 500 KiB"}
	updates := make(chan func(), 16)
	app.dispatchUpdate = func(update func()) { updates <- update }
	t.Cleanup(app.closeFolderWatch)
	tester.Frame()
	if err := tester.Click("开始监听"); err != nil {
		t.Fatal(err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session == nil || app.watch.session.ready })
	if app.watch.session == nil {
		t.Fatalf("could not start watch from Settings: %v", app.watch.err)
	}
	app.setSettingsOpen(false)
	main := ui.NewTester(app.view, 760, 660)
	status, ok := main.AccessByRole(ui.RoleStatus, "正在监听")
	if !ok || status.Offscreen {
		t.Fatalf("main window must show the running state: %+v", status)
	}
	if err := main.Click("停止监听"); err != nil {
		t.Fatal(err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session == nil })
	main.Frame()
	if _, ok := main.AccessByRole(ui.RoleButton, "停止监听"); ok {
		t.Fatal("stopping from the main window must remove the Stop control")
	}
}

func TestDesktopFolderWatchLifecycle(t *testing.T) {
	root := t.TempDir()
	in, out := filepath.Join(root, "screenshots"), filepath.Join(root, "output")
	settingsPath := filepath.Join(root, "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)
	baseline := filepath.Join(in, "existing.jpg")
	writeTestJPEG(t, baseline, 80, 60, 95)
	app := newApplication()
	app.restoreSettings()
	updates := make(chan func(), 16)
	app.dispatchUpdate = func(update func()) { updates <- update }
	t.Cleanup(app.closeFolderWatch)

	settings := folderWatchSettings{InputDir: in, OutputDir: out, Preset: "≤ 500 KiB"}
	if err := app.startFolderWatch(settings); err != nil {
		t.Fatal(err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session == nil || app.watch.session.ready })
	if app.watch.session == nil || app.watch.err != nil {
		t.Fatalf("watch failed to start: %v", app.watch.err)
	}
	if err := app.startFolderWatch(settings); err == nil {
		t.Fatal("starting an active watcher must be rejected")
	}

	incoming := filepath.Join(in, "incoming.jpg")
	original := writeTestJPEG(t, incoming, 320, 160, 100)
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.last.Input != "" })
	result := app.watch.last
	if result.Input != incoming || result.Err != nil || result.TargetBytes != 500<<10 || result.TargetSizeMissed || result.OutputBytes > 500<<10 {
		t.Fatalf("unexpected last image outcome: %+v", result)
	}
	resolvedOut, err := filepath.EvalSymlinks(out)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(result.Output); err != nil || info.Size() != result.OutputBytes || filepath.Dir(result.Output) != resolvedOut {
		t.Fatalf("unexpected watch copy %q: %v, %v", result.Output, info, err)
	}
	if got, err := os.ReadFile(incoming); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("watch must leave the original intact: %v", err)
	}

	app.stopFolderWatch()
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session == nil })
	writeTestJPEG(t, filepath.Join(in, "after-stop.jpg"), 80, 60, 95)
	reopened := newApplication()
	reopened.restoreSettings()
	if reopened.watch.settings != settings || reopened.watch.session != nil {
		t.Fatalf("restore must remember settings with the watcher stopped: %+v", reopened.watch)
	}

	// Keep a new file pending when the app closes, and verify that stopping
	// waits for the worker to exit before allowing another session.
	if err := app.startFolderWatch(settings); err != nil {
		t.Fatal(err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session == nil || app.watch.session.ready })
	if app.watch.session == nil {
		t.Fatalf("watch failed to restart: %v", app.watch.err)
	}
	writeTestJPEG(t, filepath.Join(in, "close-pending.jpg"), 80, 60, 95)
	app.closeFolderWatch()
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 1 {
		t.Fatalf("expected just the one copy after stop and close: %v, %v", entries, err)
	}
}

func awaitDesktopWatchUpdate(t *testing.T, updates <-chan func(), matched func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for !matched() {
		select {
		case update := <-updates:
			update()
		case <-deadline.C:
			t.Fatal("timed out waiting for desktop folder watcher state")
		}
	}
}
