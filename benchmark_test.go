package main

import (
	"fmt"
	"testing"
)

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
