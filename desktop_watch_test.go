package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestDesktopWatchDeleteStoppedRuleWhileLaterRuleRunning(t *testing.T) {
	root := t.TempDir()
	in2, out2 := filepath.Join(root, "in2"), filepath.Join(root, "out2")
	in3, out3 := filepath.Join(root, "in3"), filepath.Join(root, "out3")
	for _, dir := range []string{in2, in3} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settingsPath := filepath.Join(root, "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)

	app := newApplication()
	app.restoreSettings()
	updates := make(chan func(), 32)
	app.dispatchUpdate = func(update func()) { updates <- update }
	t.Cleanup(app.closeFolderWatch)

	rule2 := folderWatchRule{InputDir: in2, OutputDir: out2, Preset: "均衡"}
	rule3 := folderWatchRule{InputDir: in3, OutputDir: out3, Preset: "≤ 500 KiB"}
	app.watch.extraRules = []folderWatchRule{rule2, rule3}
	app.ensureWatchRuleIDs()
	rule3ID := app.watch.extraRuleIDs[1]

	if err := app.startWatchRuleByID(rule3ID, rule3, false, false); err != nil {
		t.Fatalf("startWatchRuleByID(rule3): %v", err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool {
		sess := app.watchSessionForRuleID(rule3ID)
		return sess != nil && sess.ready
	})

	tester := openTestSettings(t, app)
	if err := tester.Click("文件夹监听"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()

	// Stopped Rule 2 must be deletable while Rule 3 is actively running.
	var deleteButtons []ui.AccessInfo
	for _, node := range tester.AccessAll() {
		if node.Role == ui.RoleButton && node.Name == "删除" {
			deleteButtons = append(deleteButtons, node)
		}
	}
	if len(deleteButtons) != 2 {
		t.Fatalf("expected 2 delete buttons for extra rules, got %d", len(deleteButtons))
	}
	if deleteButtons[0].Disabled {
		t.Fatal("delete button for stopped earlier rule must be enabled while later rule runs")
	}
	if !deleteButtons[1].Disabled {
		t.Fatal("delete button for actively running rule must remain disabled")
	}
	if deleteButtons[0].Offscreen {
		tester.Scroll(420, 350, 0, 360)
		tester.Frame()
	}

	if err := tester.Click("删除"); err != nil {
		t.Fatalf("click delete on Rule 2: %v", err)
	}
	tester.Frame()

	if len(app.editor.watchRules) != 1 || app.editor.watchRules[0] != rule3 {
		t.Fatalf("expected only rule3 left in editor, got %+v", app.editor.watchRules)
	}
	if len(app.editor.watchRuleIDs) != 1 || app.editor.watchRuleIDs[0] != rule3ID {
		t.Fatalf("expected rule3 stable ID %d preserved in editor, got %+v", rule3ID, app.editor.watchRuleIDs)
	}
	if app.watchSessionForRuleID(rule3ID) == nil {
		t.Fatal("rule3 session must keep running after deleting stopped rule2")
	}

	app.saveSettingsEdits()
	if app.settingsOpen {
		t.Fatalf("expected settings to save and close cleanly, watchMessage=%q message=%q", app.editor.watchMessage, app.editor.message)
	}
	if len(app.watch.extraRules) != 1 || app.watch.extraRules[0] != rule3 {
		t.Fatalf("expected saved extraRules to contain only rule3, got %+v", app.watch.extraRules)
	}
	if app.watchSessionForRule(1) == nil || app.watchSessionForRule(1) != app.watchSessionForRuleID(rule3ID) {
		t.Fatal("expected rule3 session to remain bound to saved rule slot and stable ID")
	}

	// Verify rule3 still processes incoming files cleanly after rule2 was removed.
	writeTestJPEG(t, filepath.Join(in3, "after-delete.jpg"), 160, 120, 95)
	awaitDesktopWatchUpdate(t, updates, func() bool {
		sess := app.watchSessionForRuleID(rule3ID)
		return sess != nil && sess.last.Input != ""
	})
	if err := app.watchSessionForRuleID(rule3ID).last.Err; err != nil {
		t.Fatalf("rule3 failed processing after index shift: %v", err)
	}

	// Verify tray menu tracks rule3 by its stable ID and can stop it cleanly.
	menu := app.buildTrayMenu()
	trayItem := menu.ItemByID(fmt.Sprintf("tray-watch-rule-%d", rule3ID))
	if trayItem == nil || !trayItem.Checked || trayItem.Click == nil {
		t.Fatalf("expected checked tray item for rule3 ID %d, got %+v", rule3ID, trayItem)
	}
	trayItem.Click(trayItem, nil)
	awaitDesktopWatchUpdate(t, updates, func() bool {
		return app.activeFolderWatchCount() == 0
	})
}

func TestDesktopWatchConflictValidation(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	dirBSub := filepath.Join(dirB, "sub")
	dirC := filepath.Join(root, "c")
	dirOut1 := filepath.Join(root, "out1")
	dirOut2 := filepath.Join(root, "out2")
	for _, dir := range []string{dirA, dirB, dirBSub, dirC, dirOut1, dirOut2} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkA := filepath.Join(root, "link-a")
	linkB := filepath.Join(root, "link-b")
	if err := os.Symlink(dirA, linkA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dirB, linkB); err != nil {
		t.Fatal(err)
	}

	conflictCases := []struct {
		name   string
		first  folderWatchRule
		second folderWatchRule
	}{
		{
			name:   "direct loop A->B and B->A",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirB, OutputDir: dirA, Preset: "均衡"},
		},
		{
			name:   "chain A->B and B->C",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirB, OutputDir: dirC, Preset: "均衡"},
		},
		{
			name:   "recursive A->B/sub vs B->C",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirBSub, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirB, OutputDir: dirC, Preset: "均衡", Recursive: true},
		},
		{
			name:   "recursive A->B vs B/sub->C",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "均衡", Recursive: true},
			second: folderWatchRule{InputDir: dirBSub, OutputDir: dirC, Preset: "均衡"},
		},
		{
			name:   "duplicate A->B and A->B",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "≤ 500 KiB"},
		},
		{
			name:   "symlink alias loop A->B and linkB->linkA",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirB, Preset: "均衡"},
			second: folderWatchRule{InputDir: linkB, OutputDir: linkA, Preset: "均衡"},
		},
	}

	for _, tc := range conflictCases {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			// 1. Reject on saveSettingsEdits.
			settingsPath := filepath.Join(t.TempDir(), "settings.json")
			app := newApplication()
			app.settingsFile = settingsPath
			app.setSettingsOpen(true)
			app.editor.section = settingsGeneral
			app.editor.watch.applyPrimaryRule(tc.first)
			app.editor.watchRules = []folderWatchRule{tc.second}
			app.saveSettingsEdits()
			if !app.settingsOpen {
				t.Fatalf("saveSettingsEdits must reject conflicting rules (%s)", tc.name)
			}
			if app.editor.section != settingsFolderWatch {
				t.Fatalf("expected section to switch to settingsFolderWatch, got %v", app.editor.section)
			}
			if app.editor.watchMessage == "" {
				t.Fatal("expected watchMessage to describe the conflict")
			}

			// 2. Reject on startWatchRule against an actively running rule.
			runApp := newApplication()
			updates := make(chan func(), 32)
			runApp.dispatchUpdate = func(update func()) { updates <- update }
			t.Cleanup(runApp.closeFolderWatch)
			if err := runApp.startWatchRuleWithNotify(0, tc.first, false, false); err != nil {
				t.Fatalf("start first rule: %v", err)
			}
			awaitDesktopWatchUpdate(t, updates, func() bool {
				return runApp.watch.session != nil && runApp.watch.session.ready
			})
			if err := runApp.startWatchRuleWithNotify(1, tc.second, false, false); err == nil {
				t.Fatalf("startWatchRule must reject conflicting rule (%s)", tc.name)
			}
			if runApp.watchSessionForRule(1) != nil {
				t.Fatal("conflicting second rule must not create an active session")
			}
		})
	}

	allowedCases := []struct {
		name   string
		first  folderWatchRule
		second folderWatchRule
	}{
		{
			name:   "fan-out A->Out1 and A->Out2",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirOut1, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirA, OutputDir: dirOut2, Preset: "≤ 500 KiB"},
		},
		{
			name:   "fan-in A->Out1 and B->Out1",
			first:  folderWatchRule{InputDir: dirA, OutputDir: dirOut1, Preset: "均衡"},
			second: folderWatchRule{InputDir: dirB, OutputDir: dirOut1, Preset: "≤ 500 KiB"},
		},
	}

	for _, tc := range allowedCases {
		t.Run("allow_"+tc.name, func(t *testing.T) {
			// 1. Allow on saveSettingsEdits.
			settingsPath := filepath.Join(t.TempDir(), "settings.json")
			app := newApplication()
			app.settingsFile = settingsPath
			app.setSettingsOpen(true)
			app.editor.watch.applyPrimaryRule(tc.first)
			app.editor.watchRules = []folderWatchRule{tc.second}
			app.saveSettingsEdits()
			if app.settingsOpen {
				t.Fatalf("saveSettingsEdits must allow %s, got watchMessage=%q message=%q", tc.name, app.editor.watchMessage, app.editor.message)
			}

			// 2. Allow concurrent startWatchRule.
			runApp := newApplication()
			updates := make(chan func(), 32)
			runApp.dispatchUpdate = func(update func()) { updates <- update }
			t.Cleanup(runApp.closeFolderWatch)
			if err := runApp.startWatchRuleWithNotify(0, tc.first, false, false); err != nil {
				t.Fatalf("start first rule (%s): %v", tc.name, err)
			}
			if err := runApp.startWatchRuleWithNotify(1, tc.second, false, false); err != nil {
				t.Fatalf("start second rule (%s): %v", tc.name, err)
			}
			awaitDesktopWatchUpdate(t, updates, func() bool {
				s0 := runApp.watchSessionForRule(0)
				s1 := runApp.watchSessionForRule(1)
				return s0 != nil && s0.ready && s1 != nil && s1.ready
			})
		})
	}

	selfOverlapCases := []struct {
		name string
		rule folderWatchRule
	}{
		{name: "same input and output", rule: folderWatchRule{InputDir: dirA, OutputDir: dirA, Preset: "均衡"}},
		{name: "output inside input", rule: folderWatchRule{InputDir: dirB, OutputDir: dirBSub, Preset: "均衡"}},
		{name: "recursive input inside output", rule: folderWatchRule{InputDir: dirBSub, OutputDir: dirB, Preset: "均衡", Recursive: true}},
	}
	for _, tc := range selfOverlapCases {
		t.Run("self_"+tc.name, func(t *testing.T) {
			app := newApplication()
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			app.setSettingsOpen(true)
			app.editor.section = settingsGeneral
			app.editor.watch.applyPrimaryRule(tc.rule)
			app.saveSettingsEdits()
			if !app.settingsOpen || app.editor.section != settingsFolderWatch || app.editor.watchMessage == "" {
				t.Fatalf("saveSettingsEdits must reject self-overlapping rule (%s)", tc.name)
			}
			if err := app.startWatchRuleWithNotify(0, tc.rule, false, false); err == nil {
				t.Fatalf("startWatchRule must reject self-overlapping rule (%s)", tc.name)
			}
		})
	}
}

func TestDesktopWatchPersistingStartRejectsConflictWithStoppedSavedRule(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "A")
	dirB := filepath.Join(root, "B")
	dirC := filepath.Join(root, "C")
	for _, d := range []string{dirA, dirB, dirC} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settingsPath := filepath.Join(root, "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)

	app := newApplication()
	app.restoreSettings()
	t.Cleanup(app.closeFolderWatch)

	// Save primary rule A -> B (stopped).
	primary := folderWatchSettings{
		InputDir:  dirA,
		OutputDir: dirB,
		Preset:    "均衡",
	}
	if err := app.rememberFolderWatchSettings(primary); err != nil {
		t.Fatalf("rememberFolderWatchSettings: %v", err)
	}

	// Attempting to start rule 2 (B -> C) with persist=true must fail synchronously
	// even though primary rule is not currently running.
	conflicting := folderWatchRule{
		InputDir:  dirB,
		OutputDir: dirC,
		Preset:    "均衡",
	}
	if err := app.startWatchRule(1, conflicting); err == nil {
		t.Fatal("startWatchRule(persist=true) must reject rule conflicting with stopped saved primary rule")
	}
	if app.watchSessionForRule(1) != nil {
		t.Fatal("conflicting rule must not start a session")
	}

	// Direct rememberFolderWatchRule must also refuse to persist a conflicting rule.
	if err := app.rememberFolderWatchRule(1, conflicting, false); err == nil {
		t.Fatal("rememberFolderWatchRule must reject rule conflicting with saved primary rule")
	}

	// settings.json must still contain only the clean primary rule and zero extra rules.
	loaded, err := loadFullSettings(settingsPath)
	if err != nil {
		t.Fatalf("loadFullSettings: %v", err)
	}
	if loaded.watch.primaryRule() != primary.primaryRule() {
		t.Fatalf("primary rule changed unexpectedly: %+v", loaded.watch)
	}
	if len(loaded.watchRules) != 0 {
		t.Fatalf("expected no conflicting extra watch rules persisted, got %+v", loaded.watchRules)
	}
}

func TestDesktopWatchInspectInputParentNotDirWording(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveOutputDirectory(context.Background(), filePath); err == nil {
		t.Fatal("expected error when resolved path is a regular file")
	} else if strings.Contains(err.Error(), "输出路径") || !strings.Contains(err.Error(), "父级不是文件夹") {
		t.Fatalf("unexpected resolveOutputDirectory error wording: %v", err)
	}
	if _, err := inspectWatchPath(filepath.Join(filePath, "child"), "输入目录"); err == nil {
		t.Fatal("expected error when input parent is a regular file")
	} else if strings.Contains(err.Error(), "输出路径") || !strings.Contains(err.Error(), "输入目录无效") {
		t.Fatalf("unexpected inspectWatchPath error wording: %v", err)
	}
}

func TestDesktopWatchCaseInsensitiveNonexistentPaths(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case-insensitive nonexistent watch path matching applies on darwin/windows")
	}
	root := t.TempDir()
	dirA := filepath.Join(root, "in-a")
	dirC := filepath.Join(root, "out-c")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	err := app.validateFolderWatchRules(
		folderWatchRule{InputDir: dirA, OutputDir: filepath.Join(root, "Out"), Preset: "均衡"},
		[]folderWatchRule{{InputDir: filepath.Join(root, "out"), OutputDir: dirC, Preset: "均衡"}},
		[]uint64{1},
	)
	if err == nil {
		t.Fatal("expected nonexistent /Out and /out to conflict on case-insensitive platform")
	}
}

func TestFolderWatchStatusBarRevealButton(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.jpg")
	outPath := filepath.Join(root, "out.jpg")
	writeTestJPEG(t, inPath, 64, 48, 90)
	writeTestJPEG(t, outPath, 64, 48, 80)

	var revealed []string
	app := newApplication()
	app.showInFolder = func(path string) {
		revealed = append(revealed, path)
	}

	// Hidden when Output == "" (even if status bar is visible because Input != "").
	app.watch.last = folderWatchOutcome{Input: inPath, Output: ""}
	mainTester := ui.NewTester(app.view, 980, 720)
	if _, ok := mainTester.AccessByRole(ui.RoleButton, "定位最新输出文件"); ok {
		t.Fatal("reveal button must be hidden when watch.last.Output is empty")
	}

	// Hidden when Err != nil (even if Output != "").
	app.watch.last = folderWatchOutcome{
		Input:  inPath,
		Output: outPath,
		Err:    errors.New("encode failed"),
	}
	mainTester.Frame()
	if _, ok := mainTester.AccessByRole(ui.RoleButton, "定位最新输出文件"); ok {
		t.Fatal("reveal button must be hidden when watch.last.Err is non-nil")
	}

	// Shown and clickable in main window when Output != "" and Err == nil.
	app.watch.last = folderWatchOutcome{
		Input:         inPath,
		Output:        outPath,
		OriginalBytes: 4096,
		OutputBytes:   2048,
	}
	mainTester.Frame()
	btn, ok := mainTester.AccessByRole(ui.RoleButton, "定位最新输出文件")
	if !ok || btn.Offscreen || btn.Disabled {
		t.Fatalf("reveal button must be shown and enabled in main window: ok=%v btn=%+v", ok, btn)
	}
	if err := mainTester.Click("定位最新输出文件"); err != nil {
		t.Fatalf("click main window watch reveal button: %v", err)
	}
	if len(revealed) != 1 || revealed[0] != outPath {
		t.Fatalf("clicking main window watch reveal must invoke showInFolder(%q), got %v", outPath, revealed)
	}

	// In main window, disabled whenever app.modalOpen() is true.
	for _, modal := range []struct {
		name string
		set  func(*application, bool)
	}{
		{name: "busy", set: func(a *application, on bool) { a.busy = on }},
		{name: "settingsOpen", set: func(a *application, on bool) { a.settingsOpen = on }},
		{name: "confirmOverwrite", set: func(a *application, on bool) { a.confirmOverwrite = on }},
		{name: "confirmBatchOverwrite", set: func(a *application, on bool) { a.confirmBatchOverwrite = on }},
	} {
		modal.set(app, true)
		if !app.modalOpen() {
			t.Fatalf("expected app.modalOpen() == true for %s", modal.name)
		}
		mainTester.Frame()
		btn, ok := mainTester.AccessByRole(ui.RoleButton, "定位最新输出文件")
		if !ok || !btn.Disabled {
			t.Fatalf("main window reveal button must be disabled when modalOpen (%s): ok=%v btn=%+v", modal.name, ok, btn)
		}
		modal.set(app, false)
	}

	// In settings window on settingsFolderWatch: enabled even though app.settingsOpen == true,
	// clicking reveals watch.last.Output, and disabled when app.editor.confirmDelete == true.
	app.setSettingsOpen(true)
	app.editor.section = settingsFolderWatch
	if !app.settingsOpen || !app.modalOpen() {
		t.Fatalf("expected settingsOpen && modalOpen, got settingsOpen=%v modalOpen=%v", app.settingsOpen, app.modalOpen())
	}
	settingsTester := ui.NewTester(app.settingsView, 724, 700)
	btn, ok = settingsTester.AccessByRole(ui.RoleButton, "定位最新输出文件")
	if !ok || btn.Offscreen || btn.Disabled {
		t.Fatalf("settings window watch reveal button must be enabled even when settingsOpen==true: ok=%v btn=%+v", ok, btn)
	}
	if err := settingsTester.Click("定位最新输出文件"); err != nil {
		t.Fatalf("click settings window watch reveal button: %v", err)
	}
	if len(revealed) != 2 || revealed[1] != outPath {
		t.Fatalf("clicking settings window watch reveal must invoke showInFolder(%q), got %v", outPath, revealed)
	}

	app.editor.confirmDelete = true
	settingsTester.Frame()
	btn, ok = settingsTester.AccessByRole(ui.RoleButton, "定位最新输出文件")
	if !ok || !btn.Disabled {
		t.Fatalf("settings window watch reveal button must be disabled when confirmDelete==true: ok=%v btn=%+v", ok, btn)
	}

	// Hidden in settings window when Output == "" or Err != nil.
	app.editor.confirmDelete = false
	app.watch.last.Err = errors.New("watch failed")
	settingsTester.Frame()
	if _, ok := settingsTester.AccessByRole(ui.RoleButton, "定位最新输出文件"); ok {
		t.Fatal("settings window watch reveal button must hide when watch.last.Err != nil")
	}
	app.watch.last.Err = nil
	app.watch.last.Output = ""
	settingsTester.Frame()
	if _, ok := settingsTester.AccessByRole(ui.RoleButton, "定位最新输出文件"); ok {
		t.Fatal("settings window watch reveal button must hide when watch.last.Output == \"\"")
	}
}
