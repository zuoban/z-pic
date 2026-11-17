package main

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchPipelineSavesCompletedWorkBeforeSlowFirstTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slow := make(chan struct{})
	var released atomic.Int32
	var consumed []int
	runBatchPipeline(ctx, []batchTask{{index: 0}, {index: 1}, {index: 2}}, 2,
		func(ctx context.Context, task batchTask) batchCompressionOutcome {
			if task.index == 0 {
				select {
				case <-slow:
				case <-ctx.Done():
				}
			}
			return batchCompressionOutcome{release: func() { released.Add(1) }}
		},
		func(task batchTask, outcome batchCompressionOutcome) {
			consumed = append(consumed, task.index)
			if task.index == 1 {
				close(slow)
			}
		})
	if ctx.Err() != nil || len(consumed) != 3 || consumed[0] != 1 || released.Load() != 3 {
		t.Fatalf("fast output was blocked by slow input: order=%v releases=%d error=%v", consumed, released.Load(), ctx.Err())
	}
	slices.Sort(consumed)
	if !slices.Equal(consumed, []int{0, 1, 2}) {
		t.Fatalf("each task must be consumed once: %v", consumed)
	}
}

func TestBatchPipelineCancellationDrainsAndReleasesWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var processed, released atomic.Int32
	consumed := 0
	tasks := make([]batchTask, 20)
	runBatchPipeline(ctx, tasks, 3,
		func(ctx context.Context, task batchTask) batchCompressionOutcome {
			processed.Add(1)
			return batchCompressionOutcome{release: func() { released.Add(1) }}
		},
		func(task batchTask, outcome batchCompressionOutcome) {
			consumed++
			cancel()
		})
	if consumed == 0 || int32(consumed) != processed.Load() || released.Load() != processed.Load() {
		t.Fatalf("cancellation leaked work: processed=%d consumed=%d released=%d", processed.Load(), consumed, released.Load())
	}
	runBatchPipeline(ctx, tasks, 3,
		func(ctx context.Context, task batchTask) batchCompressionOutcome {
			t.Error("canceled pipelines must not start new compression")
			return batchCompressionOutcome{}
		}, func(batchTask, batchCompressionOutcome) { t.Error("unexpected completion") })
}
