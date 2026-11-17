package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkScanBatchPaths1000Files(bench *testing.B) {
	root := bench.TempDir()
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 32, 24)), &jpeg.Options{Quality: 80}); err != nil {
		bench.Fatal(err)
	}
	paths := make([]string, 1000)
	for album := range 10 {
		dir := filepath.Join(root, "photos", "2026", fmt.Sprintf("album_%02d", album))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			bench.Fatal(err)
		}
		for photo := range 100 {
			index := album*100 + photo
			paths[index] = filepath.Join(dir, fmt.Sprintf("IMG_%04d.JPG", index))
			if err := os.WriteFile(paths[index], encoded.Bytes(), 0o600); err != nil {
				bench.Fatal(err)
			}
		}
	}
	for _, scenario := range []struct {
		name   string
		inputs []string
	}{
		{"Folder", []string{root}},
		{"FileList", paths},
	} {
		bench.Run(scenario.name, func(sub *testing.B) {
			sub.ReportAllocs()
			ctx := context.Background()
			sub.ResetTimer()
			for iteration := 0; iteration < sub.N; iteration++ {
				scan, err := scanBatchPaths(ctx, scenario.inputs, nil, 0)
				if err != nil {
					sub.Fatal(err)
				}
				if len(scan.items) != len(paths) || scan.duplicates != 0 || scan.truncated {
					sub.Fatalf("unexpected scan result: items=%d duplicates=%d truncated=%v",
						len(scan.items), scan.duplicates, scan.truncated)
				}
			}
		})
	}
}

func BenchmarkPlanBatchOutputs1000Files(bench *testing.B) {
	root := bench.TempDir()
	outputDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		bench.Fatal(err)
	}
	tasks := make([]batchTask, 1000)
	for index := range tasks {
		dir := filepath.Join(root, "photos", "2026", fmt.Sprintf("album_%04d", index))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			bench.Fatal(err)
		}
		path := filepath.Join(dir, "photo.jpg")
		if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
			bench.Fatal(err)
		}
		tasks[index] = batchTask{index: index, path: path}
	}
	for _, name := range []string{"UniqueNames", "SameName"} {
		bench.Run(name, func(sub *testing.B) {
			for index := range tasks {
				tasks[index].relPath = "photo.jpg"
				if name == "UniqueNames" {
					tasks[index].relPath = fmt.Sprintf("photo_%04d.jpg", index)
				}
			}
			sub.ReportAllocs()
			ctx := context.Background()
			sub.ResetTimer()
			for iteration := 0; iteration < sub.N; iteration++ {
				paths, failures := planBatchOutputs(ctx, tasks, outputDir, nil)
				if len(paths) != len(tasks) || len(failures) != 0 {
					sub.Fatalf("unexpected plan: paths=%d failures=%v", len(paths), failures)
				}
			}
		})
	}
}

func BenchmarkBatchFrameQueries1000Items(bench *testing.B) {
	items := make([]batchItem, 1000)
	for index := range items {
		status := batchPending
		switch index % 4 {
		case 1:
			status = batchDone
		case 2:
			status = batchFailed
		}
		path := fmt.Sprintf("/photos/vacation/IMG_%04d.JPG", index)
		items[index] = batchItem{
			path:         path,
			absPath:      path,
			relPath:      fmt.Sprintf("vacation/IMG_%04d.JPG", index),
			format:       "jpeg",
			width:        4000,
			height:       3000,
			originalSize: 4_000_000,
			outputSize:   1_500_000,
			status:       status,
		}
	}
	appState := newApplication()
	appState.batch = newBatchState(items)
	appState.batch.query = "img_05"
	appState.batch.sort = batchSortSavings
	appState.floating.filter = batchFilterFailed

	_ = appState.filteredBatchIndices()
	_ = appState.floatingIndices()

	bench.ReportAllocs()
	bench.ResetTimer()
	for iter := 0; iter < bench.N; iter++ {
		_ = appState.filteredBatchIndices()
		_ = appState.floatingIndices()
		_, _, _, _, _, _, _ = appState.batchStats()
		_, _ = appState.batchFailureCounts()
	}
}
