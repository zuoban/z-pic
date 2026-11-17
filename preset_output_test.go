package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"zpic/internal/compress"
)

func TestPresetOutputPathsPreserveSourcesAndExtensions(t *testing.T) {
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "photo.JPEG")
	if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []presetSaveMode{presetSaveOriginal, presetSaveAsk, presetSaveFixed} {
		t.Run(string(mode), func(t *testing.T) {
			fixedDir := t.TempDir()
			o := presetSaveOptions{Mode: mode, Directory: fixedDir, UseSubfolder: true, UseSuffix: true}
			used := make(map[string]bool)
			path, err := o.copyPath(source, fixedDir, "photo.JPEG", used)
			if err != nil {
				t.Fatal(err)
			}
			root := fixedDir
			if mode == presetSaveOriginal {
				root = sourceDir
			}
			if mode == presetSaveOriginal || mode == presetSaveFixed {
				root = filepath.Join(root, "zpic")
			}
			want := filepath.Join(root, "photo-zpic.JPEG")
			if path != want {
				t.Fatalf("path = %s, want %s", path, want)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{SourcePath: source, Data: []byte("copy")}
			if err := result.Save(path); err != nil {
				t.Fatal(err)
			}
			next, err := o.copyPath(source, fixedDir, "photo.JPEG", make(map[string]bool))
			if err != nil || next == path {
				t.Fatalf("existing output must not be replaced: %s, %v", next, err)
			}
		})
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "original" {
		t.Fatalf("source changed: %q, %v", data, err)
	}
}

func TestPresetOutputValidationAndFixedErrors(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", `a\b`, "..", "a\n"} {
		for _, o := range []presetSaveOptions{{Mode: presetSaveOriginal, UseSubfolder: true, Subfolder: name}, {Mode: presetSaveFixed, UseSubfolder: true, Subfolder: name}, {UseSuffix: true, Suffix: name}} {
			if o.validate() == nil {
				t.Fatalf("accepted unsafe output: %+v", o)
			}
		}
	}
	if err := (presetSaveOptions{Mode: presetSaveAsk, UseSubfolder: true, Subfolder: "../hidden"}).validate(); err != nil {
		t.Fatalf("ask mode must ignore hidden subfolder preference: %v", err)
	}
	if err := (presetSaveOptions{Mode: presetSaveFixed}).validate(); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		_, err := (presetSaveOptions{Mode: presetSaveFixed}).copyPath("photo.png", root, "photo.png", make(map[string]bool))
		if err == nil {
			t.Fatalf("accepted unavailable root %q", root)
		}
	}
}

func TestPresetOutputKeepsSingleResultAndOnlyResetsChangedBatch(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result
	s.applyPresetOutput(presetSaveOptions{UseSuffix: true})
	if s.result != result {
		t.Fatal("output change must keep single result")
	}
	s.batch = newBatchState([]batchItem{{status: batchDone, savedPath: "/out/photo.jpg"}})
	o := presetSaveOptions{Mode: presetSaveFixed, Directory: t.TempDir()}
	s.applyPresetOutput(o)
	if s.batch.items[0].status != batchPending {
		t.Fatal("output change must reset batch")
	}
	s.batch.items[0].status = batchDone
	s.applyPresetOutput(o)
	if s.batch.items[0].status != batchDone {
		t.Fatal("same output must retain batch results")
	}
}

func TestPresetOriginalSaveRequestsConfirmation(t *testing.T) {
	s := testSelectedApplication()
	s.applyPresetOutput(presetSaveOptions{Mode: presetSaveOriginal})
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.savePresetResult()
	if !s.confirmOverwrite || s.busy || s.overwritten {
		t.Fatal("saving to original must request confirmation before writing")
	}
	s.confirmOverwrite = false
	s.result.Unchanged = true
	s.savePresetResult()
	if s.confirmOverwrite {
		t.Fatal("unchanged result must not request replacement")
	}
}

func TestPresetOriginalSubfolderSavesWithoutConfirmation(t *testing.T) {
	source := filepath.Join(t.TempDir(), "photo.jpg")
	original := writeTestJPEG(t, source, 64, 48, 95)
	s := testSelectedApplication()
	s.file.path = source
	s.file.size = int64(len(original))
	s.applyPresetOutput(presetSaveOptions{Mode: presetSaveOriginal, UseSubfolder: true, Subfolder: "分享"})
	result, err := compress.Bytes(context.Background(), original, source, s.compressionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged || len(result.Data) == 0 {
		t.Fatal("fixture must produce an encoded JPEG result")
	}
	s.result = result
	updates := make(chan func(), 1)
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.savePresetResult()
	if s.confirmOverwrite || s.overwritten || !s.busy {
		t.Fatal("subfolder save must start a copy without overwrite confirmation")
	}
	s.saves.Wait()
	select {
	case update := <-updates:
		update()
	default:
		t.Fatal("save must dispatch its completion")
	}
	want := filepath.Join(filepath.Dir(source), "分享", "photo.jpg")
	data, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(data, result.Data) {
		t.Fatalf("saved copy differs from encoded result: %v", err)
	}
	data, err = os.ReadFile(source)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatalf("subfolder save changed source: %v", err)
	}
	if s.confirmOverwrite || s.overwritten || s.busy || s.failed || s.phase != "" || s.lastSavedPath != want || s.message != "已保存："+want {
		t.Fatalf("incorrect completed save state: confirm=%v overwritten=%v busy=%v failed=%v phase=%q path=%q message=%q", s.confirmOverwrite, s.overwritten, s.busy, s.failed, s.phase, s.lastSavedPath, s.message)
	}
}

func TestBatchPresetCopiesUseEachSourceDirectory(t *testing.T) {
	s := newApplication()
	var items []batchItem
	originals := make(map[string][]byte)
	for range 2 {
		path := filepath.Join(t.TempDir(), "photo.jpg")
		data := writeTestJPEG(t, path, 64, 48, 95)
		items = append(items, batchItem{path: path, originalSize: int64(len(data)), relPath: "photo.jpg"})
		originals[path] = data
	}
	s.batch = newBatchState(items)
	s.applyPresetOutput(presetSaveOptions{Mode: presetSaveOriginal, UseSubfolder: true})
	tasks, used := s.collectBatchTasks()
	s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, s.batch.outputDir, used, func(fn func()) { fn() })
	for _, item := range s.batch.items {
		want := filepath.Join(filepath.Dir(item.path), "zpic", "photo.jpg")
		if item.status != batchDone || item.savedPath != want {
			t.Fatalf("unexpected batch result: %+v", item)
		}
		if item.overwritten || s.confirmBatchOverwrite {
			t.Fatal("subfolder copies must not overwrite batch sources")
		}
		data, err := os.ReadFile(item.path)
		if err != nil || !bytes.Equal(data, originals[item.path]) {
			t.Fatalf("batch source changed: %s, %v", item.path, err)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBatchPresetOutputDirectoryRespectsOriginalSubfolderMode(t *testing.T) {
	for _, mode := range []presetSaveMode{presetSaveAsk, presetSaveFixed} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "in", "photo.jpg")
			original := writeTestJPEG(t, source, 64, 48, 95)
			outDir := filepath.Join(root, "out")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatal(err)
			}
			s := newApplication()
			s.batch = newBatchState([]batchItem{{path: source, originalSize: int64(len(original)), relPath: "photo.jpg"}})
			output := presetSaveOptions{Mode: mode, Directory: outDir, UseSubfolder: true, Subfolder: "分享", UseSuffix: true}
			s.applyPresetOutput(output)
			s.batch.outputMode, s.batch.outputDir = batchOutputFolder, outDir
			tasks, used := s.collectBatchTasks()
			if len(tasks) != 1 || tasks[0].output != output {
				t.Fatalf("task must snapshot the original output options: %+v", tasks)
			}
			s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, s.batch.outputDir, used, func(fn func()) { fn() })
			if tasks[0].output != output || s.output != output {
				t.Fatalf("export must retain original output options: task=%+v application=%+v", tasks[0].output, s.output)
			}
			want := filepath.Join(outDir, "photo-zpic.jpg")
			if mode == presetSaveFixed {
				want = filepath.Join(outDir, "分享", "photo-zpic.jpg")
			}
			item := s.batch.items[0]
			if item.status != batchDone || item.savedPath != want {
				t.Fatalf("unexpected batch result: %+v, want %s", item, want)
			}
			data, err := os.ReadFile(want)
			if err != nil || len(data) == 0 || int64(len(data)) != item.outputSize {
				t.Fatalf("saved output mismatch: size=%d item=%+v err=%v", len(data), item, err)
			}
			data, err = os.ReadFile(source)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("source changed during export: %v", err)
			}
		})
	}
}
