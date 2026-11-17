package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanBatchOutputsMixedNamesAndExistingEntries(test *testing.T) {
	root := test.TempDir()
	source := filepath.Join(test.TempDir(), "source.jpg")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("existing"), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "photo-compressed.jpg"), 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink("missing.jpg", filepath.Join(root, "photo-compressed-2.jpg")); err != nil {
		test.Fatal(err)
	}
	used := map[string]bool{
		filepath.Join(root, "photo-compressed-3.jpg"): true,
		filepath.Join(root, "photo-compressed-4.jpg"): false,
	}
	names := []string{
		"photo.jpg", "photo-compressed-4.jpg", "photo.jpg", "photo-compressed.jpg",
		"photo.jpg", "sub/photo.jpg", "sub/photo.jpg", "photo.jpg",
	}
	wantNames := []string{
		"photo-compressed-4.jpg", "photo-compressed-4-compressed.jpg", "photo-compressed-5.jpg", "photo-compressed-compressed.jpg",
		"photo-compressed-6.jpg", "sub/photo.jpg", "sub/photo-compressed.jpg", "photo-compressed-7.jpg",
	}
	tasks := make([]batchTask, len(names))
	for index, name := range names {
		tasks[index] = batchTask{index: index, path: source, relPath: filepath.FromSlash(name)}
	}
	paths, failures := planBatchOutputs(context.Background(), tasks, root, used)
	if len(paths) != len(tasks) || len(failures) != 0 {
		test.Fatalf("unexpected plan: paths=%v failures=%v", paths, failures)
	}
	for index, name := range wantNames {
		want := filepath.Join(root, filepath.FromSlash(name))
		if paths[index] != want || !used[want] {
			test.Fatalf("task %d: path=%q reserved=%v want=%q", index, paths[index], used[want], want)
		}
	}
}

func TestBatchOutputPlannerRechecksUnreservedNames(test *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "source"} {
		test.Run(kind, func(test *testing.T) {
			root := test.TempDir()
			candidate := filepath.Join(root, "photo.jpg")
			source := filepath.Join(root, "source.jpg")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(candidate, []byte("occupied"), 0o600)
			case "directory":
				err = os.Mkdir(candidate, 0o700)
			case "symlink":
				err = os.Symlink("missing.jpg", candidate)
			case "source":
				source = candidate
			}
			if err != nil {
				test.Fatal(err)
			}
			planner := &batchOutputPlanner{
				used:      make(map[string]bool),
				sequences: make(map[string]batchOutputSequence),
			}
			for _, name := range []string{"photo-compressed.jpg", "photo-compressed-2.jpg"} {
				path, err := planner.resolve(root, source, "photo.jpg")
				if err != nil || path != filepath.Join(root, name) {
					test.Fatalf("path=%q error=%v want=%q", path, err, name)
				}
			}
			if kind != "source" {
				if err := os.Remove(candidate); err != nil {
					test.Fatal(err)
				}
			}
			path, err := planner.resolve(root, filepath.Join(root, "other.jpg"), "photo.jpg")
			if err != nil || path != candidate {
				test.Fatalf("unreserved name must be checked again: path=%q error=%v", path, err)
			}
		})
	}
}

func TestBatchOutputPlannerProtectsSourceDirectoryAliases(test *testing.T) {
	root := test.TempDir()
	sourceDir := filepath.Join(root, "photos")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		test.Fatal(err)
	}
	source := filepath.Join(sourceDir, "photo.jpg")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		test.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(sourceDir, alias); err != nil {
		test.Fatal(err)
	}
	tasks := []batchTask{
		{index: 0, path: source, relPath: "photo.jpg"},
		{index: 1, path: source, relPath: "photo.jpg"},
		{index: 2, path: source, relPath: "photo.jpg"},
	}
	paths, failures := planBatchOutputs(context.Background(), tasks, alias, nil)
	if len(failures) != 0 || paths[0] != filepath.Join(alias, "photo-compressed.jpg") ||
		paths[1] != filepath.Join(alias, "photo-compressed-2.jpg") ||
		paths[2] != filepath.Join(alias, "photo-compressed-3.jpg") {
		test.Fatalf("source alias must stay protected: paths=%v failures=%v", paths, failures)
	}
}

func TestPlanBatchOutputsMaxSameNameAndRunIsolation(test *testing.T) {
	root := test.TempDir()
	tasks := make([]batchTask, maxBatchFiles)
	for index := range tasks {
		tasks[index] = batchTask{index: index, path: filepath.Join(root, "source.jpg"), relPath: "photo.jpg"}
	}
	for range 2 {
		paths, failures := planBatchOutputs(context.Background(), tasks, root, nil)
		if len(paths) != len(tasks) || len(failures) != 0 {
			test.Fatalf("unexpected plan: paths=%d failures=%v", len(paths), failures)
		}
		seen := make(map[string]bool, len(paths))
		for index := range tasks {
			path := paths[index]
			if path == "" || seen[path] {
				test.Fatalf("task %d reused a reserved path: %q", index, path)
			}
			seen[path] = true
		}
		wantLast := filepath.Join(root, fmt.Sprintf("photo-compressed-%d.jpg", len(tasks)-1))
		if paths[0] != filepath.Join(root, "photo.jpg") || paths[len(tasks)-1] != wantLast {
			test.Fatalf("unexpected naming sequence: first=%q last=%q", paths[0], paths[len(tasks)-1])
		}
	}
}

func TestPlanBatchOutputsFailureAndCancellationDoNotReserveNames(test *testing.T) {
	root := test.TempDir()
	tasks := []batchTask{
		{index: 0, path: filepath.Join(root, "source.jpg"), relPath: "photo.jpg",
			output: presetSaveOptions{UseSuffix: true, Suffix: "../invalid"}},
		{index: 1, path: filepath.Join(root, "source.jpg"), relPath: "photo.jpg"},
	}
	used := make(map[string]bool)
	paths, failures := planBatchOutputs(context.Background(), tasks, root, used)
	if len(paths) != 1 || len(failures) != 1 || failures[0] == nil || paths[1] != filepath.Join(root, "photo.jpg") || len(used) != 1 {
		test.Fatalf("failed task must not reserve a name: paths=%v failures=%v used=%v", paths, failures, used)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	paths, failures = planBatchOutputs(ctx, tasks, root, used)
	if len(paths) != 0 || len(failures) != 0 || len(used) != 1 {
		test.Fatalf("canceled plan changed reservations: paths=%v failures=%v used=%v", paths, failures, used)
	}
}

func TestBatchOutputPlannerWorksWithoutCallerMaps(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jpg")
	for _, resolve := range []func() (string, error){
		func() (string, error) { return resolveBatchOutputPath(root, source, "photo.jpg", nil) },
		func() (string, error) {
			return (presetSaveOptions{Mode: presetSaveFixed}).copyPath(source, root, "photo.jpg", nil)
		},
	} {
		path, err := resolve()
		if err != nil || path != filepath.Join(root, "photo.jpg") {
			t.Fatalf("nil reservations = %q, %v", path, err)
		}
	}
	var planner batchOutputPlanner
	for _, name := range []string{"photo.jpg", "photo-compressed.jpg", "photo-compressed-2.jpg"} {
		path, err := planner.resolve(root, source, "photo.jpg")
		if err != nil || path != filepath.Join(root, name) {
			t.Fatalf("zero value planner = %q, %v; want %q", path, err, name)
		}
	}
}
