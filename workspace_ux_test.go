package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
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

func TestMainWindowDropOptionsAndDragOverLabel(t *testing.T) {
	options := mainWindowDropOptions()
	if options.Operations != transfer.Copy {
		t.Fatalf("mainWindowDropOptions Operations = %v, want %v", options.Operations, transfer.Copy)
	}
	for _, want := range []transfer.Format{
		transfer.FileList,
		transfer.URIList,
		transfer.HTML,
		transfer.Text,
		transfer.PNG,
		transfer.Format("image/jpeg"),
		transfer.Format("image/webp"),
	} {
		if !slices.Contains(options.Formats, want) {
			t.Fatalf("mainWindowDropOptions missing format %q: %v", want, options.Formats)
		}
	}
	if gotAVIF := slices.Contains(options.Formats, transfer.Format("image/avif")); gotAVIF != compress.SupportsAVIF() {
		t.Fatalf("mainWindowDropOptions AVIF presence (%v) must match SupportsAVIF (%v)", gotAVIF, compress.SupportsAVIF())
	}

	app := newApplication()
	if got := app.mainWindowDragOverLabel(); got != "松开导入图片" {
		t.Fatalf("empty workspace drag-over label = %q, want %q", got, "松开导入图片")
	}

	app.file = &selectedImage{path: "/photos/one.png", format: "png"}
	if got := app.mainWindowDragOverLabel(); got != "松开加入批量队列" {
		t.Fatalf("single-image workspace drag-over label = %q, want %q", got, "松开加入批量队列")
	}

	app.file = nil
	app.batch = newBatchState([]batchItem{{path: "/photos/one.png", absPath: "/photos/one.png", relPath: "one.png"}})
	if got := app.mainWindowDragOverLabel(); got != "松开加入批量队列" {
		t.Fatalf("batch workspace drag-over label = %q, want %q", got, "松开加入批量队列")
	}
}

func TestMainWindowDropRoutingAndGuards(t *testing.T) {
	t.Run("promotes single image and second local file into batch queue", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "first.jpg")
		second := filepath.Join(root, "second.jpg")
		writeTestJPEG(t, first, 80, 60, 90)
		writeTestJPEG(t, second, 100, 75, 90)

		selected, err := readSelection(context.Background(), first)
		if err != nil {
			t.Fatal(err)
		}
		updates := make(chan func(), 16)
		app := newApplication()
		app.dispatchUpdate = func(fn func()) { updates <- fn }
		app.completeSelection(context.Background(), selected, nil)

		data, err := transfer.FileData(second)
		if err != nil {
			t.Fatal(err)
		}
		app.receiveMainWindowDrop(data)
		waitForDropImports(t, app, updates)

		if app.batch == nil || len(app.batch.items) != 2 || app.failed {
			t.Fatalf("expected single image + second drop promoted into 2-item batch: batch=%+v failed=%v msg=%q",
				app.batch, app.failed, app.message)
		}
		if app.batch.items[0].path != first || app.batch.items[1].path != second {
			t.Fatalf("unexpected batch item order: got [%q, %q], want [%q, %q]",
				app.batch.items[0].path, app.batch.items[1].path, first, second)
		}
	})

	t.Run("routes non-FileList web data to receiveDrop", func(t *testing.T) {
		// While busy, web drops are queued on dropQueue via receiveDrop without running local openPaths.
		app := newApplication()
		app.busy = true
		uriData := transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte("https://example.com/photo.png"))))
		htmlData := transfer.New(transfer.NewItem(transfer.Bytes(transfer.HTML, []byte(`<img src="https://example.com/banner.webp">`))))

		app.receiveMainWindowDrop(uriData)
		app.receiveMainWindowDrop(htmlData)
		if len(app.dropQueue) != 2 || app.batch != nil || app.file != nil {
			t.Fatalf("non-FileList drops must queue via receiveDrop: dropQueue=%d batch=%v file=%v",
				len(app.dropQueue), app.batch, app.file)
		}

		// When idle, HTML data URL routes through receiveDrop and imports cleanly.
		root := t.TempDir()
		src := filepath.Join(root, "inline.jpg")
		raw := writeTestJPEG(t, src, 64, 48, 90)
		inlineHTML := fmt.Sprintf(`<img src="data:image/jpeg;base64,%s">`, base64.StdEncoding.EncodeToString(raw))

		updates := make(chan func(), 16)
		idle := newApplication()
		idle.downloadDir = filepath.Join(root, "downloads")
		idle.dispatchUpdate = func(fn func()) { updates <- fn }
		idle.receiveMainWindowDrop(transfer.New(transfer.NewItem(transfer.Bytes(transfer.HTML, []byte(inlineHTML)))))
		if !idle.dropImporting {
			t.Fatal("idle web drop must enter receiveDrop / startNextDropImport")
		}
		waitForDropImports(t, idle, updates)
		if idle.batch == nil || len(idle.batch.items) != 1 || idle.failed || idle.dropFailed {
			t.Fatalf("expected inline HTML image imported into batch via receiveDrop: batch=%+v failed=%v dropFailed=%v msg=%q dropMsg=%q",
				idle.batch, idle.failed, idle.dropFailed, idle.message, idle.dropMessage)
		}
	})

	t.Run("ignores staged drag-out paths", func(t *testing.T) {
		root := t.TempDir()
		stagedDir := filepath.Join(root, "drag-stage")
		if err := os.MkdirAll(stagedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		stagedPath := filepath.Join(stagedDir, "exported.jpg")
		realPath := filepath.Join(root, "real.jpg")
		writeTestJPEG(t, stagedPath, 60, 40, 90)
		writeTestJPEG(t, realPath, 80, 60, 90)

		updates := make(chan func(), 16)
		app := newApplication()
		app.dragTempDir = stagedDir
		app.dispatchUpdate = func(fn func()) { updates <- fn }
		if !app.isDragStagedPath(stagedPath) || app.isDragStagedPath(realPath) {
			t.Fatalf("unexpected isDragStagedPath result: staged=%v real=%v",
				app.isDragStagedPath(stagedPath), app.isDragStagedPath(realPath))
		}

		stagedOnly, err := transfer.FileData(stagedPath)
		if err != nil {
			t.Fatal(err)
		}
		app.receiveMainWindowDrop(stagedOnly)
		if app.busy || app.file != nil || app.batch != nil || len(app.dropQueue) != 0 {
			t.Fatalf("staged drag-out path must be ignored: busy=%v file=%v batch=%v queue=%d",
				app.busy, app.file, app.batch, len(app.dropQueue))
		}

		// Mixed drop filters out staged path and still opens the real file.
		mixed, err := transfer.FileData(stagedPath, realPath)
		if err != nil {
			t.Fatal(err)
		}
		app.receiveMainWindowDrop(mixed)
		waitForDropImports(t, app, updates)
		if app.batch != nil || app.file == nil || app.file.path != realPath {
			t.Fatalf("mixed drop must filter staged path and open only real file: file=%+v batch=%+v", app.file, app.batch)
		}
	})

	t.Run("ignores drops when closed, draggingOut, or dialogOpen", func(t *testing.T) {
		root := t.TempDir()
		filePath := filepath.Join(root, "img.jpg")
		writeTestJPEG(t, filePath, 60, 40, 90)
		fileDrop, err := transfer.FileData(filePath)
		if err != nil {
			t.Fatal(err)
		}
		webDrop := transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte("https://example.com/a.png"))))

		guards := []struct {
			name  string
			apply func(*application)
		}{
			{name: "closed", apply: func(app *application) { app.closed = true }},
			{name: "draggingOut", apply: func(app *application) { app.draggingOut = true }},
			{name: "settingsOpen", apply: func(app *application) { app.settingsOpen = true }},
			{name: "confirmOverwrite", apply: func(app *application) { app.confirmOverwrite = true }},
			{name: "confirmBatchOverwrite", apply: func(app *application) { app.confirmBatchOverwrite = true }},
		}

		for _, g := range guards {
			t.Run(g.name, func(t *testing.T) {
				app := newApplication()
				g.apply(app)
				app.receiveMainWindowDrop(fileDrop)
				app.receiveMainWindowDrop(webDrop)
				if app.busy || app.file != nil || app.batch != nil || len(app.dropQueue) != 0 {
					t.Fatalf("guard %s must ignore main window drops: busy=%v file=%v batch=%v queue=%d",
						g.name, app.busy, app.file, app.batch, len(app.dropQueue))
				}
			})
		}
	})
}

func TestDragStagingLifecycleAndCleanup(t *testing.T) {
	newStagedApp := func(t *testing.T, root string) (*application, string) {
		t.Helper()
		srcPath := filepath.Join(root, "photo.jpg")
		raw := writeTestJPEG(t, srcPath, 80, 60, 90)
		app := newApplication()
		app.file = &selectedImage{
			path: srcPath, format: "jpeg", width: 80, height: 60, size: int64(len(raw)), data: raw,
		}
		app.result = &compress.Result{
			SourcePath:   srcPath,
			Format:       "jpeg",
			Width:        80,
			Height:       60,
			OriginalSize: int64(len(raw)),
			OutputSize:   int64(len(raw)),
			Data:         raw,
		}
		t.Cleanup(app.cleanupDragStaging)
		staged := app.stageSingleDragFile()
		if staged == "" {
			t.Fatal("expected stageSingleDragFile to create a staged file")
		}
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("expected staged file on disk: %v", err)
		}
		if app.dragStagedResult != app.result || app.dragStagedPath != staged {
			t.Fatalf("unexpected drag staging state: resultMatch=%v path=%q want=%q",
				app.dragStagedResult == app.result, app.dragStagedPath, staged)
		}
		return app, staged
	}

	assertCleaned := func(t *testing.T, app *application, staged string) {
		t.Helper()
		if app.dragStagedResult != nil || app.dragStagedPath != "" {
			t.Fatalf("expected drag staging pointers cleared, got result=%v path=%q",
				app.dragStagedResult != nil, app.dragStagedPath)
		}
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Fatalf("expected staged file %q removed, stat err=%v", staged, err)
		}
		if app.dragTempDir != "" {
			entries, err := os.ReadDir(app.dragTempDir)
			if err != nil {
				t.Fatalf("expected dragTempDir to remain readable: %v", err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "export-") {
					t.Fatalf("expected export-* subdirectory removed, still found %q", entry.Name())
				}
			}
		}
	}

	t.Run("invalidateResult cleans staged memory and files and allows future drags", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)
		savedResult := app.result

		app.invalidateResult()
		assertCleaned(t, app, staged)

		// Future drag still works smoothly using the preserved dragTempDir.
		app.result = savedResult
		staged2 := app.stageSingleDragFile()
		if staged2 == "" {
			t.Fatal("expected subsequent stageSingleDragFile to succeed after releaseDragStaging")
		}
		if _, err := os.Stat(staged2); err != nil {
			t.Fatalf("expected new staged file on disk: %v", err)
		}
	})

	t.Run("clearSingleImage cleans staged memory and files", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)

		app.clearSingleImage()
		assertCleaned(t, app, staged)
	})

	t.Run("completeSelection cleans staged memory and files", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)
		nextPath := filepath.Join(root, "next.jpg")
		nextRaw := writeTestJPEG(t, nextPath, 60, 40, 85)

		app.completeSelection(context.Background(), &selectedImage{
			path: nextPath, format: "jpeg", width: 60, height: 40, size: int64(len(nextRaw)), data: nextRaw,
		}, nil)
		assertCleaned(t, app, staged)
	})

	t.Run("completeOverwriteWithBackup cleans staged memory and files", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)

		app.completeOverwriteWithBackup(app.result, false, nil)
		assertCleaned(t, app, staged)
	})

	t.Run("loadBatch cleans staged memory and files", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)
		otherPath := filepath.Join(root, "other.jpg")
		writeTestJPEG(t, otherPath, 60, 40, 85)

		updates := make(chan func(), 8)
		app.dispatchUpdate = func(fn func()) { updates <- fn }
		app.loadBatch([]string{app.file.path, otherPath}, false)
		(<-updates)()

		if app.batch == nil || len(app.batch.items) != 2 {
			t.Fatalf("expected batch loaded with 2 items, got %+v", app.batch)
		}
		assertCleaned(t, app, staged)
	})

	t.Run("active draggingOut defers cleanup until Done when stale", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)

		// Begin drag via singleDragExportData so draggingOut becomes true.
		data := app.singleDragExportData()
		if len(data.Items()) == 0 || !app.draggingOut {
			t.Fatalf("expected active drag-out session: items=%d draggingOut=%v", len(data.Items()), app.draggingOut)
		}

		// Mid-drag invalidateResult must preserve staged file and result pointer.
		app.invalidateResult()
		if app.dragStagedResult == nil || app.dragStagedPath != staged {
			t.Fatalf("active draggingOut must preserve staged state: result=%v path=%q",
				app.dragStagedResult != nil, app.dragStagedPath)
		}
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("active draggingOut must preserve staged file on disk: %v", err)
		}

		// Completing the drag now sees s.result != s.dragStagedResult and releases everything.
		app.dragExportOptions().Done(transfer.Result{})
		if app.draggingOut {
			t.Fatal("Done must clear draggingOut")
		}
		assertCleaned(t, app, staged)
	})

	t.Run("Done preserves staged file when result is still current", func(t *testing.T) {
		root := t.TempDir()
		app, staged := newStagedApp(t, root)
		data := app.singleDragExportData()
		if len(data.Items()) == 0 || !app.draggingOut {
			t.Fatalf("expected active drag-out session: items=%d draggingOut=%v", len(data.Items()), app.draggingOut)
		}

		app.dragExportOptions().Done(transfer.Result{})
		if app.draggingOut {
			t.Fatal("Done must clear draggingOut")
		}
		if app.dragStagedResult != app.result || app.dragStagedPath != staged {
			t.Fatalf("Done must keep current staged result cached: resultMatch=%v path=%q",
				app.dragStagedResult == app.result, app.dragStagedPath)
		}
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("Done must keep current staged file on disk: %v", err)
		}

		// cleanupDragStaging removes the entire dragTempDir cleanly.
		tempDir := app.dragTempDir
		app.cleanupDragStaging()
		if app.dragTempDir != "" || app.dragStagedResult != nil || app.dragStagedPath != "" {
			t.Fatalf("cleanupDragStaging must reset all fields: dir=%q result=%v path=%q",
				app.dragTempDir, app.dragStagedResult != nil, app.dragStagedPath)
		}
		if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
			t.Fatalf("cleanupDragStaging must remove dragTempDir %q, stat err=%v", tempDir, err)
		}
	})
}
