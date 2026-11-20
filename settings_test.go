package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func openTestSettings(t *testing.T, application *application) *ui.Tester {
	t.Helper()
	if application.settingsFile == "" {
		application.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	}
	application.openPresetSettings()
	return ui.NewTester(application.settingsView, 640, 600)
}

func chooseTestPreset(t *testing.T, application *application, name string) {
	t.Helper()
	tester := openTestSettings(t, application)
	if err := tester.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem(name); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
}

func replaceTestPresetName(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	field, ok := tester.AccessByRole(ui.RoleTextField, "预设名称")
	if !ok {
		t.Fatal("settings must expose a named scheme field")
	}
	tester.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(name)
}

func TestDuplicatePresetCopiesCurrentDraft(t *testing.T) {
	cases := []struct {
		name        string
		sourceIndex int
		editSource  bool
	}{
		{name: "starting size", sourceIndex: 0},
		{name: "starting balanced", sourceIndex: 1},
		{name: "starting quality", sourceIndex: 2},
		{name: "unsaved custom", sourceIndex: 3, editSource: true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			application := testSelectedApplication()
			application.presets = append(application.presets, compressionPreset{
				name: "已保存方案", quality: 75, png: compress.DefaultPNGOptions(),
			})
			openTestSettings(t, application)
			editor := application.editor
			editor.selected = scenario.sourceIndex
			if scenario.editSource {
				editor.selectedPreset().name = "未保存参数"
				application.editQualityLevel(2)
			}
			editor.selectedPreset().output = presetSaveOptions{Mode: presetSaveAsk, UseSuffix: true, Suffix: "_分享"}
			source := *editor.selectedPreset()
			originals := slices.Clone(editor.presets)
			published := slices.Clone(application.presets)
			active := editor.active
			editor.message = "旧提示"
			application.duplicatePreset()
			expected := source
			expected.name = source.name + " 副本"
			if len(editor.presets) != len(originals)+1 || editor.selected != len(originals) ||
				*editor.selectedPreset() != expected || editor.active != active ||
				editor.message != "" || editor.confirmDelete ||
				!reflect.DeepEqual(editor.presets[:len(originals)], originals) {
				t.Fatalf("copy must append and select an independent custom draft: editor=%+v", editor)
			}
			application.editQualityLevel(0)
			editedPNG := compress.PNGOptions{MinQuality: 20, MaxQuality: 40, Colors: 64, Speed: 5}
			editor.selectedPreset().name = "编辑后的副本"
			if editor.selectedPreset().quality != 40 || editor.selectedPreset().png != editedPNG ||
				!reflect.DeepEqual(editor.presets[:len(originals)], originals) ||
				!reflect.DeepEqual(application.presets, published) {
				t.Fatal("editing a copy must leave the source draft and published schemes unchanged")
			}
		})
	}
}

func TestDuplicatePresetUniqueNames(t *testing.T) {
	longName := strings.Repeat("图", 40)
	cases := []struct {
		name      string
		source    string
		conflicts []string
		expected  string
	}{
		{name: "plain", source: "手机分享", expected: "手机分享 副本"},
		{name: "trimmed drafts", source: " 手机分享 ", conflicts: []string{" 手机分享 副本 ", "\t手机分享 副本 2\n"}, expected: "手机分享 副本 3"},
		{name: "first available suffix", source: "手机分享", conflicts: []string{"手机分享 副本", "手机分享 副本 3"}, expected: "手机分享 副本 2"},
		{name: "exact limit", source: strings.Repeat("图", 37), expected: strings.Repeat("图", 37) + " 副本"},
		{name: "long Chinese", source: longName, expected: strings.Repeat("图", 37) + " 副本"},
		{name: "long Unicode", source: strings.Repeat("图📷", 20), expected: strings.Repeat("图📷", 18) + "图 副本"},
		{name: "long collision", source: longName, conflicts: []string{strings.Repeat("图", 37) + " 副本", strings.Repeat("图", 35) + " 副本 2"}, expected: strings.Repeat("图", 35) + " 副本 3"},
		{name: "two digit suffix", source: longName, conflicts: []string{
			strings.Repeat("图", 37) + " 副本",
			strings.Repeat("图", 35) + " 副本 2", strings.Repeat("图", 35) + " 副本 3",
			strings.Repeat("图", 35) + " 副本 4", strings.Repeat("图", 35) + " 副本 5",
			strings.Repeat("图", 35) + " 副本 6", strings.Repeat("图", 35) + " 副本 7",
			strings.Repeat("图", 35) + " 副本 8", strings.Repeat("图", 35) + " 副本 9",
		}, expected: strings.Repeat("图", 34) + " 副本 10"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			application := newApplication()
			openTestSettings(t, application)
			application.addPreset()
			sourceIndex := application.editor.selected
			application.editor.selectedPreset().name = scenario.source
			for _, name := range scenario.conflicts {
				application.addPreset()
				application.editor.selectedPreset().name = name
			}
			application.editor.selected = sourceIndex
			application.duplicatePreset()
			actual := application.editor.selectedPreset().name
			if actual != scenario.expected || !utf8.ValidString(actual) || utf8.RuneCountInString(actual) > 40 {
				t.Fatalf("unexpected unique name: got=%q want=%q", actual, scenario.expected)
			}
			for _, existing := range application.editor.presets[:application.editor.selected] {
				if strings.TrimSpace(existing.name) == actual {
					t.Fatalf("copy name conflicts with draft %q", existing.name)
				}
			}
		})
	}
}

func TestDuplicatePresetCancelSaveAndRestart(t *testing.T) {
	for _, exit := range []string{"取消", "Escape", "保存"} {
		t.Run(exit, func(t *testing.T) {
			application := testSelectedApplication()
			application.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			t.Setenv("ZPIC_CONFIG_FILE", application.settingsFile)
			if err := saveSettings(application.settingsFile, application.presets, application.scheme); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(application.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			application.result = result
			presets := slices.Clone(application.presets)
			scheme, options, message := application.scheme, application.compressionOptions(), application.message
			tester := openTestSettings(t, application)
			active := application.editor.active
			application.editor.selected = 0
			application.duplicatePreset()
			application.editQualityLevel(2)
			copyPreset := *application.editor.selectedPreset()
			tester.Frame()
			afterCopy, err := os.ReadFile(application.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			if application.editor.active != active || !bytes.Equal(before, afterCopy) ||
				!reflect.DeepEqual(application.presets, presets) || application.scheme != scheme ||
				application.compressionOptions() != options || application.result != result || application.message != message {
				t.Fatal("copying and editing must preserve active state, result, published presets and disk bytes")
			}
			if exit == "Escape" {
				tester.Key(0, ui.KeyEscape)
			} else if err := tester.Click(exit); err != nil {
				t.Fatal(err)
			}
			if application.settingsOpen || application.editor != nil || application.scheme != scheme ||
				application.compressionOptions() != options || application.result != result {
				t.Fatal("closing settings must retain the active scheme and compression result")
			}
			expected := presets
			if exit == "保存" {
				expected = append(slices.Clone(presets), copyPreset)
			} else {
				afterCancel, err := os.ReadFile(application.settingsFile)
				if err != nil || !bytes.Equal(before, afterCancel) {
					t.Fatalf("cancel must preserve disk bytes: %v", err)
				}
			}
			restarted := newApplication()
			restarted.restoreSettings()
			if !reflect.DeepEqual(application.presets, expected) || !reflect.DeepEqual(restarted.presets, expected) ||
				restarted.scheme != scheme || restarted.compressionOptions() != options {
				t.Fatal("restart must restore only saved presets and the unchanged active selection")
			}
			if exit == "保存" {
				restarted.setScheme(copyPreset.name)
				if restarted.quality != copyPreset.quality || restarted.png != copyPreset.png {
					t.Fatal("saved copy must be selectable with all copied parameters after restart")
				}
			}
		})
	}
}

func TestDuplicatePresetGuards(t *testing.T) {
	for _, guard := range []string{"busy", "pending deletion", "no editor", "negative selection", "past end", "empty presets"} {
		t.Run(guard, func(t *testing.T) {
			application := newApplication()
			openTestSettings(t, application)
			application.editor.message = "保留提示"
			switch guard {
			case "busy":
				application.busy = true
			case "pending deletion":
				application.editor.confirmDelete = true
			case "no editor":
				application.editor = nil
			case "negative selection":
				application.editor.selected = -1
			case "past end":
				application.editor.selected = len(application.editor.presets)
			case "empty presets":
				application.editor.presets = nil
			}
			var expected *settingsEditor
			if application.editor != nil {
				snapshot := *application.editor
				snapshot.presets = slices.Clone(snapshot.presets)
				expected = &snapshot
			}
			application.duplicatePreset()
			if !reflect.DeepEqual(application.editor, expected) {
				t.Fatal("guarded copy must leave the editor unchanged")
			}
			if _, err := os.Stat(application.settingsFile); !os.IsNotExist(err) {
				t.Fatalf("guarded copy must not create a settings file: %v", err)
			}
		})
	}
}

func TestPresetMaintenanceSurvivesApplicationRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", path)
	application := newApplication()
	application.restoreSettings()
	tester := openTestSettings(t, application)
	if err := tester.Click("新增预设"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "手机分享")
	application.editQualityLevel(2)
	options := compress.PNGOptions{MinQuality: 50, MaxQuality: 75, Colors: 192, Speed: 3}
	if len(application.presets) != 1 {
		t.Fatal("new drafts must not appear among active schemes before saving")
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.settingsOpen || len(application.presets) != 2 {
		t.Fatal("saving must close settings and publish the maintained scheme list")
	}
	chooseTestPreset(t, application, "手机分享")
	if application.quality != 70 || application.png != options {
		t.Fatal("main scheme selection must apply the saved JPEG and PNG parameters")
	}
	restarted := newApplication()
	restarted.restoreSettings()
	if restarted.scheme != "手机分享" || restarted.quality != 70 || restarted.png != options ||
		!reflect.DeepEqual(restarted.presets, application.presets) {
		t.Fatal("saved scheme definitions and the last selection must survive application restart")
	}
	tester = openTestSettings(t, restarted)
	replaceTestPresetName(t, tester, "发送图片")
	if err := tester.Click("新增预设"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "临时方案")
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tester = openTestSettings(t, restarted)
	if err := tester.Click("编辑预设：临时方案"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("删除预设"); err != nil {
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
	if again.scheme != "发送图片" || again.quality != 70 || again.png != options || len(again.presets) != 2 {
		t.Fatal("renaming the active custom scheme and deleting another custom scheme must survive restart")
	}
	for _, preset := range again.presets {
		if preset.name == "手机分享" || preset.name == "临时方案" {
			t.Fatalf("renamed and deleted entries must not return after restart: %+v", again.presets)
		}
	}
}

func TestCancelPresetEditsPreservesSavedConfiguration(t *testing.T) {
	for _, exit := range []string{"取消", "Escape"} {
		t.Run(exit, func(t *testing.T) {
			application := testSelectedApplication()
			application.presets = append(application.presets, compressionPreset{
				name: "自定义方案", quality: 80, png: compress.DefaultPNGOptions(),
			})
			application.setScheme("自定义方案")
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
			application.editQualityLevel(0)
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
	application.presets = append(application.presets, compressionPreset{
		name: "自定义方案", quality: 80, png: compress.DefaultPNGOptions(),
	})
	application.setScheme("自定义方案")
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	application.settingsFile = t.TempDir()
	original := slices.Clone(application.presets)
	tester := openTestSettings(t, application)
	replaceTestPresetName(t, tester, "待保存方案")
	application.editQualityLevel(1)
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
	for _, name := range []string{"", "均衡"} {
		t.Run("name="+name, func(t *testing.T) {
			application := newApplication()
			application.presets = append(application.presets, compressionPreset{
				name: "自定义方案", quality: 80, png: compress.DefaultPNGOptions(),
			})
			application.setScheme("自定义方案")
			expected := slices.Clone(application.presets)
			tester := openTestSettings(t, application)
			replaceTestPresetName(t, tester, name)
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if !application.settingsOpen || application.editor.message == "" ||
				!reflect.DeepEqual(application.presets, expected) {
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
	application.presets = append(application.presets, compressionPreset{
		name: "日常备份", quality: 80, png: compress.DefaultPNGOptions(),
	})
	application.setScheme("日常备份")
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
	if err != nil || selected != "日常压缩" || presets[3].name != "日常压缩" {
		t.Fatalf("the trimmed name and selection must be saved: selected=%q err=%v", selected, err)
	}
}

func TestEditingInactivePresetPreservesActiveCompressionResult(t *testing.T) {
	application := testSelectedApplication()
	application.presets = append(application.presets, compressionPreset{
		name: "待调整方案", quality: 60, png: compress.DefaultPNGOptions(),
	})
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	tester := openTestSettings(t, application)
	if err := tester.Click("编辑预设：待调整方案"); err != nil {
		t.Fatal(err)
	}
	application.editQualityLevel(2)
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.result != result || application.scheme != "均衡" || application.quality != 80 ||
		application.presets[3].quality != 70 {
		t.Fatal("saving an inactive scheme must not invalidate the active result or change active parameters")
	}
}

func TestDeletingActivePresetSelectsValidFallback(t *testing.T) {
	application := testSelectedApplication()
	application.presets = append(application.presets, compressionPreset{
		name: "临时方案", quality: 73, png: compress.DefaultPNGOptions(),
	})
	application.setScheme("临时方案")
	application.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	tester := openTestSettings(t, application)
	if err := tester.Click("删除预设"); err != nil {
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
	if application.scheme != "画质优先" || application.quality != 92 || application.result != nil ||
		len(application.presets) != 3 {
		t.Fatal("deleting the active scheme must choose a valid fallback and invalidate a changed result")
	}
	_, selected, err := loadSettings(application.settingsFile)
	if err != nil || selected != "画质优先" {
		t.Fatalf("the fallback selection must be saved: selected=%q err=%v", selected, err)
	}
}

func TestStartingPresetsCanBeDeletedUntilOneRemains(t *testing.T) {
	application := newApplication()
	if len(application.presets) != 1 || application.presets[0].name != "均衡" ||
		application.presets[0].quality != 80 || application.presets[0].png != compress.DefaultPNGOptions() {
		t.Fatalf("the initial preset must be balanced: %+v", application.presets)
	}
	tester := openTestSettings(t, application)
	initialDelete, ok := tester.AccessByRole(ui.RoleButton, "删除预设")
	if !ok || !initialDelete.Disabled {
		t.Fatal("the single default preset must not be deletable")
	}
	application.addPreset()
	application.addPreset()
	tester.Frame()
	for len(application.editor.presets) > 1 {
		button, ok := tester.AccessByRole(ui.RoleButton, "删除预设")
		if !ok || button.Disabled {
			t.Fatal("each preset must be deletable while another preset remains")
		}
		if err := tester.Click("删除预设"); err != nil {
			t.Fatal(err)
		}
		if err := tester.Click("确认删除"); err != nil {
			t.Fatal(err)
		}
	}
	button, ok := tester.AccessByRole(ui.RoleButton, "删除预设")
	if !ok || !button.Disabled {
		t.Fatal("the last remaining preset must not be deletable")
	}
	application.editor.confirmDelete = true
	application.deletePreset()
	if len(application.editor.presets) != 1 {
		t.Fatal("the delete action must preserve the final preset")
	}
	application.editor.confirmDelete = false
	remaining := *application.editor.selectedPreset()
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	loaded, selected, err := loadSettings(application.settingsFile)
	if err != nil || len(loaded) != 1 || loaded[0] != remaining || selected != remaining.name {
		t.Fatalf("deleting starting presets must survive reload: presets=%+v selected=%q err=%v", loaded, selected, err)
	}
}

func TestStartingPresetEditsSaveWithoutChangingActiveSelection(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	tester := openTestSettings(t, application)
	for _, label := range []string{"新增预设", "复制预设", "删除预设"} {
		button, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || button.Disabled {
			t.Fatalf("settings must expose an enabled %q action", label)
		}
	}
	if err := tester.Click("编辑预设：体积优先"); err != nil {
		t.Fatal(err)
	}
	name, ok := tester.AccessByRole(ui.RoleTextField, "预设名称")
	if !ok || name.Disabled {
		t.Fatal("starting preset names must be editable")
	}
	replaceTestPresetName(t, tester, "社交分享")
	application.editQualityLevel(2)
	png := compress.PNGOptions{MinQuality: 50, MaxQuality: 75, Colors: 192, Speed: 3}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if application.result != result || application.scheme != "均衡" || application.quality != 80 {
		t.Fatal("saving another starting preset must preserve the active selection and result")
	}
	loaded, selected, err := loadSettings(application.settingsFile)
	if err != nil || selected != "均衡" || loaded[0].name != "社交分享" || loaded[0].quality != 70 || loaded[0].png != png {
		t.Fatalf("all edited starting preset parameters must survive reload: presets=%+v selected=%q err=%v", loaded, selected, err)
	}
}

func TestOpeningSettingsAgainKeepsUnsavedDraft(t *testing.T) {
	application := newApplication()
	openTestSettings(t, application)
	application.addPreset()
	application.editQualityLevel(0)
	editor := application.editor
	application.setSettingsOpen(true)
	if application.editor != editor || application.editor.presets[application.editor.selected].quality != 40 {
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
	if !application.failed || !strings.Contains(application.message, "读取压缩预设失败") ||
		application.scheme != "均衡" || len(application.presets) != 1 || application.presets[0].name != "均衡" ||
		application.presets[0].quality != 80 || application.presets[0].png != compress.DefaultPNGOptions() {
		t.Fatal("a corrupt startup configuration must use defaults and expose its error")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, invalid) {
		t.Fatal("recovering defaults must not overwrite the invalid configuration")
	}
	application.setScheme("均衡")
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(after, invalid) {
		t.Fatal("selecting the fallback scheme must not overwrite a corrupt configuration")
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
	if err != nil || selected != "均衡" {
		t.Fatalf("an explicit save must repair the configuration: selected=%q err=%v", selected, err)
	}
}

func TestFailedSelectionSavePreservesActiveSchemeAndResult(t *testing.T) {
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result, application.settingsFile = result, t.TempDir()
	application.setScheme("体积优先")
	if application.scheme != "均衡" || application.quality != 80 || application.result != result ||
		!application.failed || !strings.Contains(application.message, "保存预设选择失败") {
		t.Fatal("a failed selection save must leave the previous scheme and valid result intact")
	}
}

func TestNewPresetUsesBalancedDefaultsAndUniqueName(t *testing.T) {
	application := newApplication()
	if len(application.presets) != 1 || application.presets[0].name != "均衡" ||
		application.presets[0].quality != 80 || application.presets[0].png != compress.DefaultPNGOptions() {
		t.Fatalf("new presets must start from the single balanced default: %+v", application.presets)
	}
	openTestSettings(t, application)
	active := application.editor.active
	application.addPreset()
	preset := *application.editor.selectedPreset()
	if len(application.editor.presets) != 2 || len(application.presets) != 1 ||
		preset.name != "新预设" || preset.quality != 80 || preset.png != compress.DefaultPNGOptions() ||
		preset.output != (presetSaveOptions{}) || application.editor.active != active {
		t.Fatalf("new presets must use balanced parameters without replacing the active preset: %+v", preset)
	}
	application.editor.selectedPreset().name = " 新预设 "
	application.addPreset()
	if application.editor.selectedPreset().name != "新预设 2" {
		t.Fatal("new preset names must avoid trimmed draft names")
	}
	application.addPreset()
	if application.editor.selectedPreset().name != "新预设 3" {
		t.Fatal("successive new presets must receive unique names")
	}
}

func TestImageQualityLevelsControlAllFormats(t *testing.T) {
	if len(imageQualityLevels) != 6 {
		t.Fatal("image quality must provide six levels")
	}
	for index, level := range imageQualityLevels {
		if level.label == "" || level.png.Validate() != nil || level.quality < 1 || level.quality > 100 {
			t.Fatalf("level %d must have a label and valid encoding parameters: %+v", index, level)
		}
		if index > 0 {
			previous := imageQualityLevels[index-1]
			if level.quality <= previous.quality || level.png.MinQuality < previous.png.MinQuality ||
				level.png.MaxQuality < previous.png.MaxQuality || level.png.Colors < previous.png.Colors ||
				level.png.Speed > previous.png.Speed {
				t.Fatalf("level %d must increase quality consistently across all formats", index)
			}
		}
	}
	for _, expected := range []struct {
		index   int
		quality float64
		png     compress.PNGOptions
	}{
		{1, 60, compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
		{3, 80, compress.DefaultPNGOptions()},
		{4, 92, compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
	} {
		level := imageQualityLevels[expected.index]
		if level.quality != expected.quality || level.png != expected.png {
			t.Fatalf("level %d must preserve the starting preset parameters: %+v", expected.index, level)
		}
	}
	if imageQualityLevels[3].label != "均衡质量" {
		t.Fatal("the balanced level must be named 均衡质量")
	}
	application := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	application.result = result
	openTestSettings(t, application)
	application.editor.selected = 0
	for index, level := range imageQualityLevels {
		application.editQualityLevel(index)
		preset := *application.editor.selectedPreset()
		if preset.quality != level.quality || preset.png != level.png || qualityLevel(preset) != index {
			t.Fatalf("level %d must update JPEG, WebP and PNG together: %+v", index, preset)
		}
		if application.editor.active != 1 || application.scheme != "均衡" || application.quality != 80 ||
			application.result != result || !reflect.DeepEqual(application.presets, testLegacyPresets()) {
			t.Fatal("editing a level must remain an isolated draft until saved")
		}
	}
}

func TestQualityLevelConsidersPNGAndJPEGForAdvancedValues(t *testing.T) {
	cases := []struct {
		quality float64
		png     compress.PNGOptions
		want    int
	}{
		{79, compress.PNGOptions{MinQuality: 64, MaxQuality: 84, Colors: 250, Speed: 4}, 3},
		{80, compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}, 1},
		{60, compress.DefaultPNGOptions(), 3},
	}
	for _, scenario := range cases {
		preset := compressionPreset{quality: scenario.quality, png: scenario.png}
		if actual := qualityLevel(preset); actual != scenario.want {
			t.Fatalf("nearest level must consider all encoding parameters: preset=%+v got=%d want=%d", preset, actual, scenario.want)
		}
	}
}

func TestEditQualityLevelGuards(t *testing.T) {
	for _, guard := range []string{"busy", "no editor", "negative selection", "past selection", "negative level", "past level"} {
		t.Run(guard, func(t *testing.T) {
			application := newApplication()
			openTestSettings(t, application)
			level := 0
			switch guard {
			case "busy":
				application.busy = true
			case "no editor":
				application.editor = nil
			case "negative selection":
				application.editor.selected = -1
			case "past selection":
				application.editor.selected = len(application.editor.presets)
			case "negative level":
				level = -1
			case "past level":
				level = 6
			}
			var expected *settingsEditor
			if application.editor != nil {
				snapshot := *application.editor
				snapshot.presets = slices.Clone(snapshot.presets)
				expected = &snapshot
			}
			application.editQualityLevel(level)
			if !reflect.DeepEqual(application.editor, expected) {
				t.Fatal("an unavailable editor or out of range level must leave the draft unchanged")
			}
		})
	}
}

func TestPresetOutputSaveCancelAndRestart(t *testing.T) {
	for _, save := range []bool{false, true} {
		name := "cancel"
		if save {
			name = "save"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			t.Setenv("ZPIC_CONFIG_FILE", path)
			application := testSelectedApplication()
			application.settingsFile = path
			if err := saveSettings(path, application.presets, application.scheme); err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			application.result = result
			openTestSettings(t, application)
			output := presetSaveOptions{Mode: presetSaveAsk, UseSuffix: true, Suffix: "_分享"}
			application.editor.selectedPreset().output = output
			if application.output != (presetSaveOptions{}) {
				t.Fatal("output edits must remain in the draft")
			}
			if save {
				application.saveSettingsEdits()
			} else {
				application.setSettingsOpen(false)
			}
			restarted := newApplication()
			restarted.restoreSettings()
			expected := presetSaveOptions{}
			if save {
				expected = output
			}
			if application.settingsOpen || application.editor != nil || application.result != result ||
				application.output != expected || restarted.output != expected ||
				!reflect.DeepEqual(application.presets, restarted.presets) {
				t.Fatal("only saved output settings must survive restart without discarding compressed data")
			}
		})
	}
}

func TestPresetSubfolderAvailableForOriginalAndFixed(t *testing.T) {
	application := newApplication()
	tester := openTestSettings(t, application)
	draft := application.editor.selectedPreset()
	draft.output.Directory = t.TempDir()
	draft.output.UseSubfolder, draft.output.Subfolder = true, "分享"
	tester.Frame()
	for _, step := range []struct {
		label   string
		visible bool
	}{
		{"原文件夹", true},
		{"每次询问", false},
		{"固定输出目录", true},
		{"原文件夹", true},
		{"每次询问", false},
		{"固定输出目录", true},
		{"原文件夹", true},
	} {
		if err := tester.Click(step.label); err != nil {
			t.Fatal(err)
		}
		checkbox, hasCheckbox := tester.AccessByRole(ui.RoleCheckBox, "保存到子文件夹")
		field, hasField := tester.AccessByRole(ui.RoleTextField, "子文件夹名称")
		if hasCheckbox != step.visible || hasField != step.visible {
			t.Fatalf("subfolder controls must only appear for original or fixed directories: mode=%s checkbox=%v field=%v", step.label, hasCheckbox, hasField)
		}
		if step.visible && (!checkbox.Checked || checkbox.Disabled || field.Disabled || field.Value != "分享") {
			t.Fatalf("returning to a supported directory must restore editable subfolder preferences: checkbox=%+v field=%+v", checkbox, field)
		}
		if !draft.output.UseSubfolder || draft.output.Subfolder != "分享" {
			t.Fatal("switching destinations must retain the subfolder preference")
		}
		if application.output != (presetSaveOptions{}) {
			t.Fatal("destination edits must remain in the draft until saved")
		}
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := loadSettings(application.settingsFile)
	if err != nil || len(loaded) != 1 || loaded[0].output != application.output ||
		application.output.Mode != presetSaveOriginal || !application.output.UseSubfolder || application.output.Subfolder != "分享" {
		t.Fatalf("saving must preserve the original-folder subfolder preference: loaded=%+v output=%+v err=%v", loaded, application.output, err)
	}
}
