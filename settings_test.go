package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func openTestSettings(t *testing.T, application *application) *ui.Tester {
	t.Helper()
	if application.settingsFile == "" {
		application.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	}
	application.setSettingsOpen(true)
	return ui.NewTester(application.settingsView, 760, 560)
}

func replaceTestPresetName(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	field, ok := tester.AccessByRole(ui.RoleTextField, "方案名称")
	if !ok {
		t.Fatal("settings must expose a named scheme field")
	}
	tester.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(name)
}

func TestPresetMaintenanceSurvivesApplicationRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", path)
	application := newApplication()
	application.restoreSettings()
	tester := openTestSettings(t, application)
	if err := tester.Click("新增方案"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "手机分享")
	application.editQuality(73)
	options := compress.PNGOptions{MinQuality: 30, MaxQuality: 70, Colors: 96, Speed: 5}
	application.editPNGOptions(options)
	if len(application.presets) != 3 {
		t.Fatal("new drafts must not appear among active schemes before saving")
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.settingsOpen || len(application.presets) != 4 {
		t.Fatal("saving must close settings and publish the maintained scheme list")
	}
	main := ui.NewTester(application.view, 880, 660)
	if err := main.Click("手机分享"); err != nil {
		t.Fatal(err)
	}
	if application.quality != 73 || application.png != options {
		t.Fatal("main scheme selection must apply the saved JPEG and PNG parameters")
	}
	restarted := newApplication()
	restarted.restoreSettings()
	if restarted.scheme != "手机分享" || restarted.quality != 73 || restarted.png != options ||
		!reflect.DeepEqual(restarted.presets, application.presets) {
		t.Fatal("saved scheme definitions and the last selection must survive application restart")
	}
	tester = openTestSettings(t, restarted)
	replaceTestPresetName(t, tester, "发送图片")
	if err := tester.Click("编辑方案：体积优先"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("删除方案"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	again := newApplication()
	again.restoreSettings()
	if again.scheme != "发送图片" || again.quality != 73 || again.png != options || len(again.presets) != 3 {
		t.Fatal("renaming the active scheme and deleting another scheme must survive restart")
	}
	for _, preset := range again.presets {
		if preset.name == "手机分享" || preset.name == "体积优先" {
			t.Fatalf("renamed and deleted entries must not return after restart: %+v", again.presets)
		}
	}
}

func TestCancelPresetEditsPreservesSavedConfiguration(t *testing.T) {
	for _, exit := range []string{"取消", "Escape"} {
		t.Run(exit, func(t *testing.T) {
			application := testSelectedApplication()
			application.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			if err := saveSettings(application.settingsFile, application.presets, application.scheme); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(application.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			application.result = result
			options, presets, message := application.compressionOptions(), slices.Clone(application.presets), application.message
			tester := openTestSettings(t, application)
			replaceTestPresetName(t, tester, "未保存的方案")
			application.editQuality(35)
			application.addPreset()
			if exit == "Escape" {
				tester.Key(0, ui.KeyEscape)
			} else if err := tester.Click(exit); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(application.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			if application.settingsOpen || application.editor != nil || application.result != result ||
				application.compressionOptions() != options || application.message != message ||
				!reflect.DeepEqual(application.presets, presets) || !bytes.Equal(before, after) {
				t.Fatal("canceling settings must discard draft edits without changing active state or saved bytes")
			}
		})
	}
}

func TestSettingsSaveFailureKeepsDraftAndCompressionResult(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	application.settingsFile = t.TempDir()
	original := slices.Clone(application.presets)
	tester := openTestSettings(t, application)
	replaceTestPresetName(t, tester, "待保存方案")
	application.editQuality(45)
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if !application.settingsOpen || application.editor == nil ||
		application.editor.presets[application.editor.selected].name != "待保存方案" ||
		!strings.Contains(application.editor.message, "无法保存设置") {
		t.Fatal("a disk write failure must retain the editor and explain the error")
	}
	if application.result != result || application.quality != 80 || !reflect.DeepEqual(application.presets, original) {
		t.Fatal("failed settings saves must preserve the active schemes and valid result")
	}
	if _, ok := tester.AccessByRole(ui.RoleAlert, application.editor.message); !ok {
		t.Fatal("settings save errors must be visible and announced within the editor")
	}
}

func TestInvalidPresetNamePreventsPublishingAndSaving(t *testing.T) {
	for _, name := range []string{"", "体积优先"} {
		t.Run("name="+name, func(t *testing.T) {
			application := newApplication()
			tester := openTestSettings(t, application)
			replaceTestPresetName(t, tester, name)
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if !application.settingsOpen || application.editor.message == "" ||
				!reflect.DeepEqual(application.presets, compressionPresets) {
				t.Fatal("empty or duplicate names must keep the draft open and leave active schemes unchanged")
			}
			if _, err := os.Stat(application.settingsFile); !os.IsNotExist(err) {
				t.Fatalf("invalid scheme names must not create a settings file: %v", err)
			}
		})
	}
}

func TestRenamingActivePresetPreservesValidCompressionResult(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	tester := openTestSettings(t, application)
	replaceTestPresetName(t, tester, "  日常压缩  ")
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.settingsOpen || application.scheme != "日常压缩" || application.result != result {
		t.Fatal("renaming without changing compression parameters must retain the valid result")
	}
	presets, selected, err := loadSettings(application.settingsFile)
	if err != nil || selected != "日常压缩" || presets[1].name != "日常压缩" {
		t.Fatalf("the trimmed name and selection must be saved: selected=%q err=%v", selected, err)
	}
}

func TestEditingInactivePresetPreservesActiveCompressionResult(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	tester := openTestSettings(t, application)
	if err := tester.Click("编辑方案：体积优先"); err != nil {
		t.Fatal(err)
	}
	application.editQuality(55)
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.result != result || application.scheme != "均衡" || application.quality != 80 ||
		application.presets[0].quality != 55 {
		t.Fatal("saving an inactive scheme must not invalidate the active result or change active parameters")
	}
}

func TestDeletingActivePresetSelectsValidFallback(t *testing.T) {
	application := testSelectedApplication()
	application.setScheme("画质优先")
	application.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	tester := openTestSettings(t, application)
	if err := tester.Click("删除方案"); err != nil {
		t.Fatal(err)
	}
	save, ok := tester.AccessByRole(ui.RoleButton, "保存")
	if !ok || !save.Disabled {
		t.Fatal("saving must wait for an explicit delete decision")
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.scheme != "均衡" || application.quality != 80 || application.result != nil ||
		len(application.presets) != 2 {
		t.Fatal("deleting the active scheme must choose a valid fallback and invalidate a changed result")
	}
	_, selected, err := loadSettings(application.settingsFile)
	if err != nil || selected != "均衡" {
		t.Fatalf("the fallback selection must be saved: selected=%q err=%v", selected, err)
	}
}

func TestCannotDeleteLastPreset(t *testing.T) {
	application := newApplication()
	application.presets = []compressionPreset{compressionPresets[1]}
	tester := openTestSettings(t, application)
	button, ok := tester.AccessByRole(ui.RoleButton, "删除方案")
	if !ok || !button.Disabled {
		t.Fatal("deleting the last remaining scheme must be disabled")
	}
	application.editor.confirmDelete = true
	application.deletePreset()
	if len(application.editor.presets) != 1 {
		t.Fatal("the state method must also preserve the last scheme")
	}
}

func TestOpeningSettingsAgainKeepsUnsavedDraft(t *testing.T) {
	application := newApplication()
	openTestSettings(t, application)
	application.editQuality(35)
	editor := application.editor
	application.setSettingsOpen(true)
	if application.editor != editor || application.editor.presets[application.editor.selected].quality != 35 {
		t.Fatal("reopening an existing settings window must preserve its current draft")
	}
	application.setScheme("体积优先")
	if application.scheme != "均衡" || application.quality != 80 {
		t.Fatal("main scheme selection must remain blocked while the modal editor is open")
	}
}

func TestRestoreInvalidSettingsUsesDefaultsWithoutOverwritingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	invalid := []byte("{broken configuration")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZPIC_CONFIG_FILE", path)
	application := newApplication()
	application.restoreSettings()
	if !application.failed || !strings.Contains(application.message, "读取压缩方案失败") ||
		application.scheme != "均衡" || !reflect.DeepEqual(application.presets, compressionPresets) {
		t.Fatal("a corrupt startup configuration must use defaults and expose its error")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, invalid) {
		t.Fatal("recovering defaults must not overwrite the invalid configuration")
	}
	application.setScheme("体积优先")
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(after, invalid) {
		t.Fatal("switching fallback schemes must not overwrite a corrupt configuration")
	}
	tester := openTestSettings(t, application)
	if application.editor.message == "" {
		t.Fatal("the settings dialog must retain the startup configuration error")
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.settingsOpen || application.settingsError != "" || application.failed {
		t.Fatal("explicitly saving recovered settings must clear the startup error")
	}
	_, selected, err := loadSettings(path)
	if err != nil || selected != "体积优先" {
		t.Fatalf("an explicit save must repair the configuration: selected=%q err=%v", selected, err)
	}
}

func TestFailedSelectionSavePreservesActiveSchemeAndResult(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result, application.settingsFile = result, t.TempDir()
	application.setScheme("体积优先")
	if application.scheme != "均衡" || application.quality != 80 || application.result != result ||
		!application.failed || !strings.Contains(application.message, "保存方案选择失败") {
		t.Fatal("a failed selection save must leave the previous scheme and valid result intact")
	}
}
