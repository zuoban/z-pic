package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestWorkspacePresetEntryPreservesResult(t *testing.T) {
	app := styleReviewApplication(t, "result")
	result, source := app.result, app.file
	tester := ui.NewTester(app.view, 760, 660)
	if err := tester.Click("管理预设"); err != nil {
		t.Fatal(err)
	}
	if !app.settingsOpen || app.editor == nil || app.editor.section != settingsPresets ||
		app.editor.presets[app.editor.selected].name != app.scheme || app.file != source || app.result != result {
		t.Fatal("the contextual entry must open the active preset draft while preserving the image and result")
	}
	app.setSettingsOpen(false)
	if app.result != result || app.file != source {
		t.Fatal("canceling the contextual editor must retain the result")
	}
}

func TestWorkspaceSaveActionsDescribeDestination(t *testing.T) {
	for _, scene := range []struct {
		name        string
		output      presetSaveOptions
		label       string
		unchanged   bool
		overwritten bool
		disabled    bool
	}{
		{name: "overwrite", label: "覆盖原图…"},
		{name: "unchanged", label: "无需覆盖（未减小）", unchanged: true, disabled: true},
		{name: "overwritten", label: "已覆盖原图", overwritten: true, disabled: true},
		{name: "ask", output: presetSaveOptions{Mode: presetSaveAsk}, label: "选择位置并保存…"},
		{name: "fixed", output: presetSaveOptions{Mode: presetSaveFixed, Directory: "/Pictures/输出"}, label: "保存到预设目录"},
		{name: "source-copy", output: presetSaveOptions{UseSuffix: true, Suffix: "-small"}, label: "保存到原文件夹"},
	} {
		t.Run(scene.name, func(t *testing.T) {
			app := styleReviewApplication(t, "result")
			app.output, app.overwritten = scene.output, scene.overwritten
			app.result.Unchanged = scene.unchanged
			if scene.unchanged {
				app.result.OutputSize = app.result.OriginalSize
			}
			tester := ui.NewTester(app.view, 760, 660)
			action, ok := tester.AccessByRole(ui.RoleButton, scene.label)
			if !ok || action.Offscreen || action.Disabled != scene.disabled {
				t.Fatalf("the save action must describe its actual destination and eligibility: %+v", action)
			}
			for _, label := range []string{"另存副本", "复制图片"} {
				if _, ok := tester.AccessByRole(ui.RoleButton, label); !ok {
					t.Fatalf("save destination must retain the explicit %s action", label)
				}
			}
		})
	}
}

func TestWorkspaceOutputExampleUsesImageAndEnabledOptions(t *testing.T) {
	app := styleReviewApplication(t, "result")
	app.file.path = "/Pictures/source/海报.final.webp"
	app.output = presetSaveOptions{UseSubfolder: true, Subfolder: "发布", UseSuffix: true, Suffix: "-small"}
	tester := ui.NewTester(app.view, 760, 660)
	if !tester.HasText(filepath.Join("发布", "海报.final-small.webp")) {
		t.Fatal("the workspace must show the real extension with the active subfolder and suffix")
	}
	app.output.Mode = presetSaveAsk
	tester.Frame()
	if !tester.HasText("海报.final-small.webp") || tester.HasText(filepath.Join("发布", "海报.final-small.webp")) {
		t.Fatal("ask mode must ignore the dormant subfolder in its output example")
	}

	app = styleReviewApplication(t, "batch")
	app.batch.items[0].relPath = filepath.Join("素材", "海报.final.webp")
	app.output = presetSaveOptions{UseSubfolder: true, Subfolder: "发布", UseSuffix: true, Suffix: "-small"}
	tester = ui.NewTester(app.view, 760, 660)
	if !tester.HasText(filepath.Join("发布", "海报.final-small.webp")) ||
		tester.HasText(filepath.Join("发布", "素材", "海报.final-small.webp")) {
		t.Fatal("original-folder examples must be relative to each source file's own folder")
	}
	app.output.Mode = presetSaveFixed
	tester.Frame()
	if !tester.HasText(filepath.Join("发布", "素材", "海报.final-small.webp")) {
		t.Fatal("fixed-output examples must retain the imported relative folder")
	}
}

func TestWorkspaceScopedSearchRecovery(t *testing.T) {
	app := styleReviewApplication(t, "batch-errors")
	app.batch.filter, app.batch.sort, app.batch.query = batchFilterFailed, batchSortName, "找不到的图片"
	tester := ui.NewTester(app.view, 760, 660)
	for _, label := range []string{"全部 3", "待处理 0", "已完成 1", "失败 2"} {
		if !tester.HasText(label) {
			t.Fatalf("status counts must describe the entire queue, independent of search: %s", label)
		}
	}
	if err := tester.Click("清除搜索"); err != nil {
		t.Fatal(err)
	}
	if app.batch.query != "" || app.batch.filter != batchFilterFailed || app.batch.sort != batchSortName {
		t.Fatal("scoped recovery must clear only the query")
	}
	if !tester.Focused("筛选文件名") {
		t.Fatal("clearing search must return focus to the search field")
	}
	tester.Type("does-not-exist")
	tester.Key(0, ui.KeyEscape)
	if app.batch.query != "" || app.batch.filter != batchFilterFailed || app.batch.sort != batchSortName {
		t.Fatal("native Escape clearing must retain filter and sort")
	}
}

func TestWorkspaceBatchPreviewScopesSummaryAndShortcut(t *testing.T) {
	app := testSelectedApplication()
	source := app.file
	app.batch = newBatchState([]batchItem{
		{path: source.path, absPath: source.path, relPath: "current.jpg", originalSize: 4096, status: batchPending},
		{path: "/Pictures/other.jpg", absPath: "/Pictures/other.jpg", relPath: "other.jpg", originalSize: 1048576, status: batchPending},
	})
	app.configureBatchPresetOutput()
	app.completeBatchPreview(context.Background(), 0, app.batch.items[0], source, nil, nil, nil)
	tester := ui.NewTester(app.view, 760, 660)
	if !tester.HasText("当前图片结果") || tester.HasText("批量结果") || !tester.HasText("单张预览") {
		t.Fatal("batch preview must distinguish the current image from aggregate queue results")
	}
	tester.Key(ui.Cmd, ui.KeyEnter)
	if !app.confirmBatchOverwrite || app.pendingBatchRun != batchRunSingle || app.pendingBatchIndex != 0 ||
		app.batchRunTargetCount(app.pendingBatchRun, app.pendingBatchIndex) != 1 {
		t.Fatal("the primary shortcut in preview must target only the current image")
	}
	tester.Key(0, ui.KeyEscape)
	if app.confirmBatchOverwrite || !app.batch.previewOpen || app.batch.items[1].status != batchPending {
		t.Fatal("canceling confirmation must retain the preview and unrelated queue items")
	}
	app.result = &compress.Result{OriginalSize: 4096, OutputSize: 1024}
	tester.Frame()
	if !tester.HasText("4.0 KiB") || !tester.HasText("1.0 KiB") || tester.HasText("1.0 MiB") {
		t.Fatal("preview measurements must belong to the current result")
	}
}

func TestWorkspaceProgressIsLimitedToBatchWork(t *testing.T) {
	app := styleReviewApplication(t, "batch")
	app.busy, app.phase, app.batch.total, app.batch.done = true, "正在复制图片", 10, 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.cancel = cancel
	tester := ui.NewTester(app.view, 760, 660)
	if _, ok := tester.AccessByRole(ui.RoleProgress, "批量压缩进度"); ok {
		t.Fatal("clipboard/import work must not inherit a stale compression progress bar")
	}
	if tester.HasText("处理中 5 / 10") {
		t.Fatal("clipboard/import work must retain the queue result instead of a stale compression summary")
	}
	app.batch.running, app.phase = true, "正在压缩 6 / 10"
	tester.Frame()
	if !tester.HasText("5 / 10 · 50%") {
		t.Fatal("active batch work must expose a readable processed count and percentage")
	}
	if err := tester.Click("取消操作"); err != nil {
		t.Fatal(err)
	}
	action, ok := tester.AccessByRole(ui.RoleButton, "正在取消…")
	if !ok || !action.Disabled || ctx.Err() == nil || !tester.HasText("已完成项会保留，可继续剩余图片") {
		t.Fatal("cancellation must acknowledge the request and explain retained completed work")
	}
}

func TestWorkspaceFloatingPreviewAndFilterScope(t *testing.T) {
	app := styleReviewApplication(t, "floating")
	app.floating.expanded, app.floating.filter = true, batchFilterFailed
	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	action, ok := tester.AccessByRole(ui.RoleButton, "预览选中")
	if !ok || !action.Disabled || !tester.HasText("0 / 3 张图片") {
		t.Fatal("an empty filtered gallery must state its scope and disable preview")
	}
	app.floating.filter = batchFilterPending
	tester.Frame()
	action, ok = tester.AccessByRole(ui.RoleButton, "预览选中")
	if !ok || action.Disabled || action.Offscreen || !tester.HasText("2 / 3 张图片") {
		t.Fatal("a selected valid gallery image must have a visible preview action")
	}
	app.busy = true
	tester.Frame()
	action, _ = tester.AccessByRole(ui.RoleButton, "预览选中")
	if !action.Disabled {
		t.Fatal("floating preview must respect the shared operation guard")
	}
}

func TestWorkspaceBatchShortcutHintsMatchActions(t *testing.T) {
	app := styleReviewApplication(t, "batch")
	app.output = presetSaveOptions{Mode: presetSaveFixed, Directory: "/Pictures/output"}
	tester := ui.NewTester(app.view, 760, 660)
	if tester.HasText("输出位置") {
		t.Fatal("fixed-directory batches must not advertise an inactive output-picker shortcut")
	}
	app.output.Mode = presetSaveAsk
	tester.Frame()
	if !tester.HasText("输出位置") {
		t.Fatal("ask-mode batches must expose the output-picker shortcut")
	}
}

// Opt-in visual evidence for the interaction states added by this pass.
func TestWorkspaceUXReviewRenders(t *testing.T) {
	dir := os.Getenv("ZPIC_WORKSPACE_CAPTURES")
	if dir == "" {
		t.Skip("set ZPIC_WORKSPACE_CAPTURES to export interaction review frames")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		for _, width := range []int{760, 980} {
			for _, state := range []string{"save-copy", "save-ask", "unchanged", "overwritten", "search-empty", "failed-selected", "preview-pending", "preview-result", "batch-busy", "canceling", "large-counts"} {
				app := styleReviewApplication(t, "result")
				switch state {
				case "save-copy", "save-ask":
					app.output = presetSaveOptions{Mode: presetSaveFixed, Directory: "/Pictures/项目素材/输出目录", UseSubfolder: true, Subfolder: "发布", UseSuffix: true, Suffix: "-small"}
					if state == "save-ask" {
						app.output.Mode = presetSaveAsk
					}
				case "unchanged":
					app.result.Unchanged, app.result.OutputSize = true, app.result.OriginalSize
				case "overwritten":
					app.overwritten = true
				case "search-empty", "failed-selected", "large-counts":
					app = styleReviewApplication(t, "batch-errors")
					app.batch.selected = 1
					app.batch.items[1].errMsg = strings.Repeat("无法写入输出目录，请在预设中选择可写的文件夹。", 3)
					if state == "search-empty" {
						app.batch.filter, app.batch.query = batchFilterFailed, "找不到的图片"
					} else if state == "large-counts" {
						item := app.batch.items[0]
						for len(app.batch.items) < maxBatchFiles {
							item.path = fmt.Sprintf("/Pictures/image-%d.png", len(app.batch.items))
							item.absPath, item.relPath = item.path, filepath.Base(item.path)
							app.batch.items = append(app.batch.items, item)
						}
					}
				case "preview-pending", "preview-result":
					source, result, bitmap := app.file, app.result, app.preview
					app = styleReviewApplication(t, "batch")
					if state == "preview-pending" {
						app.batch.previewIndex, app.batch.selected = 1, 1
						app.file = &selectedImage{path: app.batch.items[1].path, format: "webp", width: 1920, height: 1080, size: 1048321, preview: bitmap}
					} else {
						app.batch.previewIndex, app.file, app.result = 0, source, result
					}
					app.batch.previewOpen, app.preview = true, bitmap
					app.comparison = comparisonView{split: 50}
				case "batch-busy", "canceling":
					app = styleReviewApplication(t, "batch")
					app.busy, app.batch.running, app.batch.total, app.batch.done = true, true, 3, 1
					app.phase, app.cancel = "正在压缩 2 / 3 · screenshot.webp", func() {}
					if state == "canceling" {
						app.cancelWork()
					}
				}
				tester := ui.NewTester(app.view, width, 720)
				tester.SetDark(dark)
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := filepath.Join(dir, fmt.Sprintf("%s-%d-%s", state, width, theme))
				file, err := os.Create(name + ".png")
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(file, tester.Image())
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("export PNG: %v / %v", err, closeErr)
				}
				data, err := json.MarshalIndent(tester.AccessAll(), "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name+".json", data, 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
