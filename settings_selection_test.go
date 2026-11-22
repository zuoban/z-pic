package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestSettingsPresetSelectionDraftLifecycle(t *testing.T) {
	for _, exit := range []string{"保存", "取消", "Escape", "close", "failure"} {
		t.Run(exit, func(t *testing.T) {
			app := testSelectedApplication()
			path := filepath.Join(t.TempDir(), "settings.json")
			app.settingsFile = path
			if err := app.saveCurrentSettings(app.presets, app.scheme, "", app.theme); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			app.result = result
			app.setSettingsOpen(true)
			tester := ui.NewTester(app.settingsView, 640, 600)
			if err := tester.Click("选择预设"); err != nil {
				t.Fatal(err)
			}
			if err := tester.ChooseMenuItem("画质优先"); err != nil {
				t.Fatal(err)
			}
			if app.scheme != "均衡" || app.result != result || app.editor.active != 2 || app.editor.selected != 2 ||
				!tester.HasText("保存后使用：画质优先") {
				t.Fatal("selection must update the draft and explain the pending change")
			}
			if err := tester.Click("管理预设"); err != nil {
				t.Fatal(err)
			}
			if app.editor.section != settingsPresets || app.editor.selectedPreset().name != "画质优先" {
				t.Fatal("preset management must edit the pending selection")
			}
			if err := tester.Click("通用"); err != nil {
				t.Fatal(err)
			}
			switch exit {
			case "Escape":
				tester.Key(0, ui.KeyEscape)
			case "close":
				app.setSettingsOpen(false)
			case "failure":
				app.settingsFile = t.TempDir()
				if err := tester.Click("保存"); err != nil {
					t.Fatal(err)
				}
			default:
				if err := tester.Click(exit); err != nil {
					t.Fatal(err)
				}
			}
			if exit == "保存" {
				cfg, err := loadFullSettings(path)
				if err != nil || cfg.selected != "画质优先" || !reflect.DeepEqual(cfg.presets, app.presets) ||
					app.scheme != "画质优先" || app.quality != 92 || app.result != nil || app.settingsOpen {
					t.Fatal("save must persist and apply the selected preset")
				}
			} else {
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) || app.scheme != "均衡" || app.result != result {
					t.Fatal("discard or write failure must preserve the saved selection and valid result")
				}
				if exit == "failure" {
					if !app.settingsOpen || app.editor.active != 2 || app.editor.message == "" {
						t.Fatal("write failure must retain the selected draft and show an error")
					}
				} else if app.settingsOpen || app.editor != nil {
					t.Fatal("discard must close and clear the settings draft")
				}
			}
		})
	}
}

func TestSettingsPresetSelectionTracksMaintenance(t *testing.T) {
	app := testSelectedApplication()
	tester := openTestSettings(t, app)
	if _, ok := tester.AccessByRole(ui.RoleMenuButton, "选择预设"); ok {
		t.Fatal("preset management section must not render the redundant top preset picker")
	}
	if err := tester.Click("新增预设"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "新的预设")
	if err := tester.Click("设为当前预设"); err != nil {
		t.Fatal(err)
	}
	if app.editor.active != app.editor.selected || app.scheme != "均衡" {
		t.Fatal("the visible activation action must select the edited draft without publishing")
	}
	if err := tester.Click("通用"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if item, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "新的预设"); !ok || !item.Checked {
		t.Fatal("the picker must include renamed drafts and mark the pending active preset")
	}
	tester.Key(0, ui.KeyEscape)
	if err := tester.Click("预设"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("删除预设"); err != nil {
		t.Fatal(err)
	}
	if navTab, ok := tester.AccessByRole(ui.RoleTab, "通用"); !ok || !navTab.Disabled {
		t.Fatal("pending deletion must disable section navigation")
	}
	app.selectSettingsPreset(0)
	if app.editor.active != 3 {
		t.Fatal("selection must remain unchanged while deletion is pending")
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if app.editor.active >= len(app.editor.presets) {
		t.Fatal("deleting the active draft must choose a valid fallback")
	}
	if err := tester.Click("通用"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "新的预设"); ok {
		t.Fatal("deleted drafts must disappear from the picker")
	}
}

func TestSettingsPresetSelectionFitsLongNames(t *testing.T) {
	for _, width := range []int{420, 640, 724} {
		for _, section := range []settingsSection{settingsGeneral, settingsPresets} {
			app := testSelectedApplication()
			name := strings.Repeat("长", 40)
			app.presets[1].name = name
			app.scheme = name
			app.setSettingsOpen(true)
			app.editor.section = section
			tester := ui.NewTester(app.settingsView, width, 600)
			if section == settingsGeneral {
				picker, ok := tester.AccessByRole(ui.RoleMenuButton, "选择预设")
				if !ok || picker.Offscreen || picker.Bounds.X < 0 ||
					picker.Bounds.X+picker.Bounds.W > float32(width) {
					t.Fatalf("long names must fit at width %d: %+v", width, picker)
				}
				if err := tester.Click("选择预设"); err != nil {
					t.Fatal(err)
				}
				if item, ok := tester.AccessByRole(ui.RoleMenuItemRadio, name); !ok || !item.Checked {
					t.Fatal("long names must keep their complete accessible labels")
				}
				tester.Key(0, ui.KeyEscape)
			} else {
				if _, ok := tester.AccessByRole(ui.RoleMenuButton, "选择预设"); ok {
					t.Fatalf("preset section must not render redundant picker at width %d", width)
				}
				if err := tester.Click("编辑预设：体积优先"); err != nil {
					t.Fatal(err)
				}
				control, ok := tester.AccessByRole(ui.RoleButton, "设为当前预设")
				if !ok || control.Offscreen || control.Bounds.X < 0 ||
					control.Bounds.X+control.Bounds.W > float32(width) ||
					control.Bounds.Y+control.Bounds.H > 600 {
					t.Fatalf("activation must be visible beside the editable name at width %d: %+v", width, control)
				}
			}
		}
	}
}
