package main

import (
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
			want := filepath.Join(root, "zipic", "photo-zipic.JPEG")
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
		for _, o := range []presetSaveOptions{{UseSubfolder: true, Subfolder: name}, {UseSuffix: true, Suffix: name}} {
			if o.validate() == nil {
				t.Fatalf("accepted unsafe output: %+v", o)
			}
		}
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

func TestBatchPresetCopiesUseEachSourceDirectory(t *testing.T) {
	s := newApplication()
	var items []batchItem
	for range 2 {
		path := filepath.Join(t.TempDir(), "photo.jpg")
		data := writeTestJPEG(t, path, 64, 48, 95)
		items = append(items, batchItem{path: path, originalSize: int64(len(data)), relPath: "photo.jpg"})
	}
	s.batch = newBatchState(items)
	s.applyPresetOutput(presetSaveOptions{UseSubfolder: true, UseSuffix: true})
	tasks, used := s.collectBatchTasks()
	s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, s.batch.outputDir, used, func(fn func()) { fn() })
	for _, item := range s.batch.items {
		want := filepath.Join(filepath.Dir(item.path), "zipic", "photo-zipic.jpg")
		if item.status != batchDone || item.savedPath != want {
			t.Fatalf("unexpected batch result: %+v", item)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatal(err)
		}
	}
}
