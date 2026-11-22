package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopWatchRememberExtraRuleSkipsEmptyPlaceholdersAndPersistsNotify(t *testing.T) {
	root := t.TempDir()
	in3, out3 := filepath.Join(root, "in3"), filepath.Join(root, "out3")
	if err := os.MkdirAll(in3, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)

	app := newApplication()
	app.restoreSettings()
	updates := make(chan func(), 32)
	app.dispatchUpdate = func(update func()) { updates <- update }
	var notifications []string
	app.sendNotification = func(title, body string) {
		notifications = append(notifications, title+"|"+body)
	}
	t.Cleanup(app.closeFolderWatch)

	if app.watch.settings.Notify {
		t.Fatal("expected Notify to start disabled")
	}

	rule3 := folderWatchRule{
		InputDir:  in3,
		OutputDir: out3,
		Preset:    "≤ 500 KiB",
		Recursive: true,
		AutoStart: true,
	}

	// Start rule 3 (ruleIndex == 2, extraIdx == 1) while rule 2 (extraIdx == 0) is empty,
	// with notifications toggled on.
	if err := app.startWatchRuleWithNotify(2, rule3, true, true); err != nil {
		t.Fatalf("startWatchRuleWithNotify(2): %v", err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool {
		sess := app.watchSessionForRule(2)
		return sess != nil && sess.ready
	})

	if app.watch.err != nil {
		t.Fatalf("unexpected watch error: %v", app.watch.err)
	}
	if !app.watch.settings.Notify {
		t.Fatal("starting an extra rule with notifyEnabled=true must immediately update s.watch.settings.Notify")
	}
	// In-memory extraRules stays aligned with ruleSessions[2].
	if len(app.watch.extraRules) != 2 || app.watch.extraRules[1] != rule3 {
		t.Fatalf("expected in-memory extraRules aligned at index 1, got %+v", app.watch.extraRules)
	}
	// Calling startAllFolderWatches must not duplicate the running session at ruleIndex 2.
	app.startAllFolderWatches()
	if got := app.activeFolderWatchCount(); got != 1 {
		t.Fatalf("expected still 1 active watch session after startAllFolderWatches, got %d", got)
	}

	// Disk persistence must omit unconfigured empty placeholder rules and persist Notify=true.
	loaded, err := loadFullSettings(settingsPath)
	if err != nil {
		t.Fatalf("loadFullSettings: %v", err)
	}
	if !loaded.watch.Notify {
		t.Fatal("expected persisted watch.Notify=true")
	}
	if len(loaded.watchRules) != 1 || loaded.watchRules[0] != rule3 {
		t.Fatalf("expected disk watch_rules to contain only configured rule3 without empty placeholders, got %+v", loaded.watchRules)
	}

	// Verify desktop notification actually fires immediately for the extra rule.
	writeTestJPEG(t, filepath.Join(in3, "shot.jpg"), 160, 120, 95)
	awaitDesktopWatchUpdate(t, updates, func() bool {
		sess := app.watchSessionForRule(2)
		return sess != nil && sess.last.Input != ""
	})
	if len(notifications) == 0 {
		t.Fatal("expected notification to fire immediately after starting extra rule with Notify enabled")
	}
}

func TestDesktopWatchBatchStartPreservesFirstError(t *testing.T) {
	runCase := func(t *testing.T, name string, trigger func(*application)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			in1, out1 := filepath.Join(root, "in1"), filepath.Join(root, "out1")
			in2, out2 := filepath.Join(root, "in2"), filepath.Join(root, "out2")
			if err := os.MkdirAll(in1, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(in2, 0o755); err != nil {
				t.Fatal(err)
			}

			app := newApplication()
			updates := make(chan func(), 32)
			app.dispatchUpdate = func(update func()) { updates <- update }
			t.Cleanup(app.closeFolderWatch)

			// Rule 0 fails synchronously with an unavailable preset; Rule 1 succeeds.
			app.watch.settings = folderWatchSettings{
				InputDir:  in1,
				OutputDir: out1,
				Preset:    "不存在的预设",
				AutoStart: true,
			}
			app.watch.extraRules = []folderWatchRule{
				{
					InputDir:  in2,
					OutputDir: out2,
					Preset:    "≤ 500 KiB",
					AutoStart: true,
				},
			}

			trigger(app)

			if app.watch.err == nil || !strings.Contains(app.watch.err.Error(), "所选监听预设已不可用") {
				t.Fatalf("expected first rule error to be preserved, got %v", app.watch.err)
			}
			if app.watchSessionForRule(0) != nil {
				t.Fatal("invalid primary rule must not have started")
			}
			awaitDesktopWatchUpdate(t, updates, func() bool {
				sess := app.watchSessionForRule(1)
				return sess != nil && sess.ready
			})
			if app.watch.err == nil || !strings.Contains(app.watch.err.Error(), "所选监听预设已不可用") {
				t.Fatalf("expected first rule error to remain after valid rule becomes ready, got %v", app.watch.err)
			}
		})
	}

	runCase(t, "startAutoFolderWatches", func(app *application) {
		app.startAutoFolderWatches()
	})
	runCase(t, "startAllFolderWatches", func(app *application) {
		app.startAllFolderWatches()
	})
}

func TestDesktopWatchExtraRuleInvalidPresetWarning(t *testing.T) {
	app := newApplication()
	tester := openTestSettings(t, app)
	if err := tester.Click("文件夹监听"); err != nil {
		t.Fatal(err)
	}

	// Primary rule has a valid preset; extra rule has an unavailable preset.
	app.editor.watch.Preset = "均衡"
	app.editor.watchRules = []folderWatchRule{
		{
			InputDir:  t.TempDir(),
			OutputDir: t.TempDir(),
			Preset:    "已删除的预设",
		},
	}
	tester.Frame()

	alert, ok := tester.AccessByRole(ui.RoleAlert, "所选监听预设已不可用，请重新选择")
	if !ok {
		t.Fatalf("expected invalid preset danger alert for extra rule, got none")
	}
	_ = alert

	// Once extra rule uses a valid preset, the alert disappears.
	app.editor.watchRules[0].Preset = "均衡"
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleAlert, "所选监听预设已不可用，请重新选择"); ok {
		t.Fatal("invalid preset alert must disappear once extra rule preset is valid")
	}
}
