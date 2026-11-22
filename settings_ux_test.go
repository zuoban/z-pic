package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsHeadingsAndCurrentPresetValue(t *testing.T) {
	app := newApplication()
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, 640, 600)
	for _, name := range []string{"设置", "外观", "工作流"} {
		heading, ok := tester.AccessByRole(ui.RoleHeading, name)
		if !ok || heading.Level == 0 {
			t.Fatalf("%q must be a navigable heading: %+v", name, heading)
		}
	}
	picker, _ := tester.AccessByRole(ui.RoleMenuButton, "选择预设")
	if picker.Value != "均衡" {
		t.Fatalf("the picker must expose the current complete value: %+v", picker)
	}
	if err := tester.Click("系统集成"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"设置", "命令行工具", "访达右键快捷操作"} {
		heading, ok := tester.AccessByRole(ui.RoleHeading, name)
		if !ok || heading.Level == 0 {
			t.Fatalf("%q must be a navigable heading on integrations tab: %+v", name, heading)
		}
	}
	if err := tester.Click("关于"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"设置", "快捷键", "关于与更新"} {
		heading, ok := tester.AccessByRole(ui.RoleHeading, name)
		if !ok || heading.Level == 0 {
			t.Fatalf("%q must be a navigable heading on about tab: %+v", name, heading)
		}
	}
}

func TestPresetTabsKeyboardSelectionAndReveal(t *testing.T) {
	app := settingsReviewApplication(t, "many-presets")
	tester := ui.NewTester(app.settingsView, 640, 600)
	last := len(app.editor.presets) - 1
	label := "编辑预设：" + app.editor.presets[last].name
	tab, ok := tester.AccessByRole(ui.RoleTab, label)
	if !ok || !tab.Selected || tab.Offscreen {
		t.Fatalf("the selected tab must be exposed and revealed: %+v", tab)
	}
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	active := app.editor.active
	tester.Key(0, ui.KeyHome)
	if app.editor.selected != 0 || app.editor.active != active {
		t.Fatal("Home must browse the first preset without changing the active preset")
	}
	tester.Key(0, ui.KeyRight)
	if app.editor.selected != 1 {
		t.Fatal("Right must browse the next preset")
	}
	tester.Key(0, ui.KeyEnd)
	tab, _ = tester.AccessByRole(ui.RoleTab, label)
	if app.editor.selected != last || tab.Offscreen || !tester.Focused(label) {
		t.Fatalf("End must focus and reveal the last preset: %+v", tab)
	}
}

func TestNewAndDuplicatePresetsImmediatelyAcceptReplacementNames(t *testing.T) {
	for _, action := range []string{"新增预设", "复制预设"} {
		t.Run(action, func(t *testing.T) {
			app := newApplication()
			tester := openTestSettings(t, app)
			if err := tester.Click(action); err != nil {
				t.Fatal(err)
			}
			if !tester.Focused("预设名称") {
				t.Fatal("the new preset's name must take focus")
			}
			tester.Type("活动图片")
			if got := app.editor.selectedPreset().name; got != "活动图片" {
				t.Fatalf("typing must replace the suggested name, got %q", got)
			}
			if app.editor.active != 0 || len(app.presets) != len(defaultPresets()) {
				t.Fatal("renaming a new draft must preserve the active and stored presets")
			}
		})
	}
}

func TestPresetNameInlineValidationAndRecovery(t *testing.T) {
	for _, name := range []string{"", "  ", "均衡", strings.Repeat("图", 41), "项目\n图片"} {
		t.Run(name, func(t *testing.T) {
			app := testSelectedApplication()
			tester := openTestSettings(t, app)
			app.editor.selected = 0
			app.editor.selectedPreset().name = name
			tester.Frame()
			field, _ := tester.AccessByRole(ui.RoleTextField, "预设名称")
			if !field.Invalid || field.Description == "" {
				t.Fatalf("invalid names need an associated explanation before saving: %+v", field)
			}
			if strings.TrimSpace(name) == "" {
				if _, ok := tester.AccessByRole(ui.RoleTab, "编辑预设：未命名预设"); !ok {
					t.Fatal("an empty draft must retain a discoverable tab")
				}
			}
			replaceTestPresetName(t, tester, " 已修正 ")
			field, _ = tester.AccessByRole(ui.RoleTextField, "预设名称")
			if field.Invalid {
				t.Fatal("inline validation must clear on correction and allow save-time trimming")
			}
		})
	}
}

func TestSaveRevealsAndFocusesInvalidPresetField(t *testing.T) {
	for _, scenario := range []string{"name", "suffix", "subfolder", "quality"} {
		t.Run(scenario, func(t *testing.T) {
			app := testSelectedApplication()
			tester := openTestSettings(t, app)
			preset := &app.editor.presets[2]
			field := "预设名称"
			switch scenario {
			case "name":
				preset.name = ""
			case "suffix":
				preset.output.UseSuffix, preset.output.Suffix = true, "/bad"
				field = "文件名后缀"
			case "subfolder":
				preset.output.UseSubfolder, preset.output.Subfolder = true, ".."
				field = "子文件夹名称"
			case "quality":
				preset.quality = 0
				field = "图片质量"
			}
			app.editor.section = settingsGeneral
			tester.Frame()
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if app.editor.selected != 2 || app.editor.section != settingsPresets || !tester.Focused(field) {
				t.Fatalf("failed save must reveal the offending preset and focus %q; editor=%+v", field, app.editor)
			}
			if !slices.Equal(app.presets, testLegacyPresets()) || app.scheme != "均衡" {
				t.Fatal("validation must retain the live configuration")
			}
		})
	}
}

func TestPendingPresetDeletionLocksDraftAndEscapeKeepsEdits(t *testing.T) {
	app := testSelectedApplication()
	tester := openTestSettings(t, app)
	app.editor.selectedPreset().name = "未保存的名称"
	tester.Frame()
	if err := tester.Click("删除预设"); err != nil {
		t.Fatal(err)
	}
	if !tester.Focused("保留预设") {
		t.Fatal("the safe deletion choice must take focus")
	}
	before := *app.editor
	before.presets = slices.Clone(before.presets)
	for _, operation := range []func(){
		app.addPreset, app.duplicatePreset, func() { app.editQualityLevel(0) },
		func() { app.selectSettingsPreset(0) }, func() { app.previewSettingsTheme(themeDark) },
	} {
		operation()
		if !reflect.DeepEqual(*app.editor, before) {
			t.Fatal("a pending confirmation must lock draft operations and preserve its target")
		}
	}
	for _, info := range tester.AccessAll() {
		switch info.Role {
		case ui.RoleTextField, ui.RoleTab, ui.RoleSlider, ui.RoleCheckBox, ui.RoleRadio:
			if !info.Disabled {
				t.Fatalf("draft controls must be disabled during confirmation: %+v", info)
			}
		}
	}
	for _, label := range []string{"新增预设", "复制预设", "删除预设", "保存"} {
		info, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || !info.Disabled {
			t.Fatalf("maintenance must be locked: %q %+v", label, info)
		}
	}
	tester.Key(0, ui.KeyEscape)
	if app.editor == nil || app.editor.confirmDelete || app.editor.selectedPreset().name != "未保存的名称" ||
		!tester.Focused("删除预设") {
		t.Fatal("first Escape must dismiss deletion and retain the settings draft")
	}
	tester.Key(0, ui.KeyEscape)
	if app.settingsOpen || app.editor != nil {
		t.Fatal("second Escape must discard and close settings")
	}
}

func TestOutputExampleFollowsEnabledNamingAndDestination(t *testing.T) {
	app := newApplication()
	tester := openTestSettings(t, app)
	preset := app.editor.selectedPreset()
	preset.output = presetSaveOptions{UseSubfolder: true, Subfolder: "发布图片", UseSuffix: true, Suffix: "-web"}
	tester.Frame()
	for _, step := range []struct {
		mode   presetSaveMode
		folder bool
		suffix bool
		want   string
	}{
		{presetSaveOriginal, true, true, "发布图片/example-web.png"},
		{presetSaveAsk, true, true, "example-web.png"},
		{presetSaveFixed, true, false, "发布图片/example.png"},
		{presetSaveOriginal, false, false, "example.png"},
	} {
		preset.output.Mode, preset.output.UseSubfolder, preset.output.UseSuffix = step.mode, step.folder, step.suffix
		tester.Frame()
		if !tester.HasText(step.want) || presetOutputExample(preset.output) != step.want {
			t.Fatalf("combined preview must reflect the actual enabled options: %+v", step)
		}
	}
	if !tester.HasText("当前设置将替换原图") {
		t.Fatal("the overwrite warning must remain explicit")
	}
}

func TestQualitySliderExposesMeaningAndNativeKeyboardControls(t *testing.T) {
	app := newApplication()
	tester := openTestSettings(t, app)
	slider, _ := tester.AccessByRole(ui.RoleSlider, "图片质量")
	tester.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	for _, step := range []struct {
		key   ui.Key
		level int
	}{
		{ui.KeyEnd, 5}, {ui.KeyLeft, 4}, {ui.KeyHome, 0}, {ui.KeyRight, 1},
	} {
		tester.Key(0, step.key)
		info, _ := tester.AccessByRole(ui.RoleSlider, "图片质量")
		if qualityLevel(*app.editor.selectedPreset()) != step.level ||
			!strings.Contains(info.Value, imageQualityLevels[step.level].label) ||
			!strings.Contains(info.Description, "JPEG / WebP") {
			t.Fatalf("slider must expose its quality level and keyboard-adjusted value: %+v", info)
		}
		if app.quality != 80 || app.png != compressionPresets[0].png {
			t.Fatal("keyboard adjustment must remain draft-only")
		}
	}
}

func TestSettingsDirtyFeedbackTracksRealChanges(t *testing.T) {
	for _, section := range []settingsSection{settingsGeneral, settingsPresets} {
		app := newApplication()
		tester := openTestSettings(t, app)
		app.editor.section = section
		tester.Frame()
		if app.settingsHaveChanges() || tester.HasText("有未保存的更改") {
			t.Fatal("opening or browsing settings must not create a dirty status")
		}
		original := *app.editor.selectedPreset()
		app.editQualityLevel(4)
		tester.Frame()
		if !tester.HasText("有未保存的更改") || !app.settingsHaveChanges() {
			t.Fatal("quality edits must be visible in the shared footer")
		}
		*app.editor.selectedPreset() = original
		tester.Frame()
		if app.settingsHaveChanges() || tester.HasText("有未保存的更改") {
			t.Fatal("reverting the draft must clear the dirty status")
		}
	}
}

func TestSettingsLargeTextKeepsControlsAndContentReachable(t *testing.T) {
	for _, size := range [][2]int{{640, 600}, {724, 700}} {
		for _, section := range []settingsSection{settingsGeneral, settingsPresets, settingsAbout} {
			app := newApplication()
			app.setSettingsOpen(true)
			app.editor.section = section
			tester := ui.NewTesterWith(app.settingsView, size[0], size[1], ui.TesterOptions{
				Preferences: ui.Preferences{TextScale: 1.5, ReduceMotion: true},
			})
			for _, label := range []string{"保存", "取消"} {
				info, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || info.Offscreen || info.Bounds.H < 48 || info.Bounds.X < 0 ||
					info.Bounds.X+info.Bounds.W > float32(size[0]) ||
					info.Bounds.Y+info.Bounds.H > float32(size[1]) {
					t.Fatalf("large-text actions must fit and retain sufficient height: %+v", info)
				}
			}
			heading, _ := tester.AccessByRole(ui.RoleHeading, "设置")
			if heading.Bounds.H < 28 {
				t.Fatal("the settings heading must honor text scaling")
			}
			save, _ := tester.AccessByRole(ui.RoleButton, "保存")
			tester.Scroll(float32(size[0])*0.7, float32(size[1])/2, 0, 2000)
			nextSave, _ := tester.AccessByRole(ui.RoleButton, "保存")
			nextHeading, _ := tester.AccessByRole(ui.RoleHeading, "设置")
			if nextSave.Bounds != save.Bounds || nextHeading.Bounds != heading.Bounds {
				t.Fatal("scrolling large content must keep navigation and actions fixed")
			}
			label := "下载目录"
			switch section {
			case settingsPresets:
				label = "元数据"
			case settingsAbout:
				label = "打开设置"
			}
			info, ok := tester.AccessByRole(ui.RoleText, label)
			if !ok || info.Offscreen || info.Bounds.Y+info.Bounds.H > save.Bounds.Y-12 {
				t.Fatalf("large-text content must remain reachable above the footer: %s %+v", label, info)
			}
		}
	}
}

func TestSettingsSidebarNavigationPreservesDraftsAndRemovesRedundantPicker(t *testing.T) {
	app := testSelectedApplication()
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, 780, 680)

	// Make draft changes across general & preset selection.
	if err := tester.Click("深色"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("画质优先"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("保存后使用：画质优先") {
		t.Fatal("general tab must show pending preset status in footer")
	}

	steps := []struct {
		label      string
		section    settingsSection
		wantText   string
		wantPicker bool
	}{
		{label: "预设", section: settingsPresets, wantText: "图片质量", wantPicker: false},
		{label: "文件夹监听", section: settingsFolderWatch, wantText: "监听预设", wantPicker: false},
		{label: "系统集成", section: settingsIntegrations, wantText: "命令行工具", wantPicker: false},
		{label: "关于", section: settingsAbout, wantText: "切换对比 / 原图 / 结果 / 差异", wantPicker: false},
		{label: "通用", section: settingsGeneral, wantText: "外观", wantPicker: true},
	}

	for _, step := range steps {
		if err := tester.Click(step.label); err != nil {
			t.Fatalf("clicking sidebar item %q failed: %v", step.label, err)
		}
		if app.editor.section != step.section {
			t.Fatalf("after clicking %q: section=%v, want %v", step.label, app.editor.section, step.section)
		}
		tab, ok := tester.AccessByRole(ui.RoleTab, step.label)
		if !ok || !tab.Selected || tab.Offscreen || tab.Bounds.X < 0 || tab.Bounds.X+tab.Bounds.W > settingsSidebarWidth {
			t.Fatalf("sidebar item %q must be selected inside the left sidebar: %+v", step.label, tab)
		}
		if !tester.HasText(step.wantText) {
			t.Fatalf("section %q missing expected content %q", step.label, step.wantText)
		}
		_, hasPicker := tester.AccessByRole(ui.RoleMenuButton, "选择预设")
		if hasPicker != step.wantPicker {
			t.Fatalf("section %q picker visibility = %v, want %v", step.label, hasPicker, step.wantPicker)
		}
		if !tester.HasText("保存后使用：画质优先") {
			t.Fatalf("section %q must keep pending preset status visible in shared footer", step.label)
		}
		if step.section == settingsAbout && !tester.HasText("1 ~ 4") {
			t.Fatal("about section shortcuts must include 1 ~ 4 preview mode keycap")
		}
		if step.section == settingsPresets {
			replaceTestPresetName(t, tester, "我的画质预设")
			if err := tester.Click("设为当前预设"); err != nil && app.editor.active != app.editor.selected {
				t.Fatal(err)
			}
			// Keep name reverted to verify draft preservation cleanly across all tabs.
			replaceTestPresetName(t, tester, "画质优先")
		}
	}

	if app.editor.theme != themeDark || app.editor.presets[app.editor.active].name != "画质优先" {
		t.Fatalf("switching through all 5 sections must preserve draft state: theme=%q active=%q",
			app.editor.theme, app.editor.presets[app.editor.active].name)
	}
}

func TestSettingsNarrowLayoutFitsAllNavigationTabs(t *testing.T) {
	const width, height = 420, 680
	app := testSelectedApplication()
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, width, height)

	navLabels := []string{"通用", "预设", "文件夹监听", "系统集成", "关于"}
	var prevRight float32
	for index, label := range navLabels {
		tab, ok := tester.AccessByRole(ui.RoleTab, label)
		if !ok || tab.Offscreen || tab.Bounds.W <= 0 || tab.Bounds.H <= 0 ||
			tab.Bounds.X < 0 || tab.Bounds.X+tab.Bounds.W > width {
			t.Fatalf("narrow nav tab %q must fit within %dpx without clipping: %+v", label, width, tab)
		}
		if index > 0 && tab.Bounds.X < prevRight {
			t.Fatalf("narrow nav tab %q overlaps previous tab: prevRight=%.1f tab=%+v", label, prevRight, tab.Bounds)
		}
		prevRight = tab.Bounds.X + tab.Bounds.W
	}

	for _, step := range []struct {
		label   string
		section settingsSection
	}{
		{"预设", settingsPresets},
		{"文件夹监听", settingsFolderWatch},
		{"系统集成", settingsIntegrations},
		{"关于", settingsAbout},
		{"通用", settingsGeneral},
	} {
		if err := tester.Click(step.label); err != nil {
			t.Fatalf("narrow click %q failed: %v", step.label, err)
		}
		if app.editor.section != step.section {
			t.Fatalf("narrow click %q: section=%v, want %v", step.label, app.editor.section, step.section)
		}
		if _, hasPicker := tester.AccessByRole(ui.RoleMenuButton, "选择预设"); hasPicker != (step.section == settingsGeneral) {
			t.Fatalf("narrow section %q picker visibility = %v, want %v", step.label, hasPicker, step.section == settingsGeneral)
		}
	}
}
