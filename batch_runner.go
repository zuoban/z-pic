package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"zpic/internal/compress"
)

func batchWorkerCount(taskCount int) int {
	if taskCount <= 1 {
		return 1
	}
	available := runtime.GOMAXPROCS(0)
	if available <= 1 {
		return 1
	}
	return min(taskCount, max(2, min(6, available/2)))
}

func batchThreadsPerWorker(workers int) int {
	if workers <= 1 {
		return 0
	}
	return min(4, max(1, runtime.GOMAXPROCS(0)/workers))
}

type batchCompressionOutcome struct {
	result  *compress.Result
	err     error
	started bool
	release func()
}

type batchCompletedTask struct {
	task    batchTask
	outcome batchCompressionOutcome
}

func runBatchPipeline(
	ctx context.Context,
	tasks []batchTask,
	workers int,
	process func(context.Context, batchTask) batchCompressionOutcome,
	consume func(batchTask, batchCompressionOutcome),
) {
	jobs := make(chan batchTask)
	completed := make(chan batchCompletedTask)
	var workerGroup sync.WaitGroup
	for range min(len(tasks), max(1, workers)) {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for task := range jobs {
				if ctx.Err() != nil {
					continue
				}
				completed <- batchCompletedTask{task: task, outcome: process(ctx, task)}
			}
		}()
	}
	workerGroup.Add(1)
	go func() {
		defer workerGroup.Done()
		defer close(jobs)
		for _, task := range tasks {
			select {
			case <-ctx.Done():
				return
			case jobs <- task:
			}
		}
	}()
	go func() {
		workerGroup.Wait()
		close(completed)
	}()
	for finished := range completed {
		consume(finished.task, finished.outcome)
		finished.outcome.result = nil
		if finished.outcome.release != nil {
			finished.outcome.release()
		}
	}
}

func planBatchOutputs(ctx context.Context, tasks []batchTask, outputDir string, usedPaths map[string]bool) (map[int]string, map[int]error) {
	if usedPaths == nil {
		usedPaths = make(map[string]bool)
	}
	planner := &batchOutputPlanner{
		ctx:       ctx,
		used:      usedPaths,
		sequences: make(map[string]batchOutputSequence),
		parents:   make(map[string]batchParentPath),
	}
	paths := make(map[int]string, len(tasks))
	failures := make(map[int]error)
	for _, task := range tasks {
		if ctx.Err() != nil {
			break
		}
		output := task.output
		output.UseSubfolder = output.supportsSubfolder() && output.UseSubfolder
		if outputDir != "" {
			output.Mode = presetSaveFixed
		}
		path, err := output.copyPathForFormatWithPlanner(task.path, outputDir, task.relPath, task.outputFormat, planner)
		if err != nil {
			failures[task.index] = err
		} else {
			paths[task.index] = path
		}
	}
	return paths, failures
}

func (s *application) runBatchTasks(
	ctx context.Context,
	tasks []batchTask,
	options compress.Options,
	outputMode int,
	outputDir string,
	usedPaths map[string]bool,
	deliver func(func()),
) {
	s.runBatchTasksWithBackup(ctx, tasks, options, outputMode, outputDir, usedPaths, s.overwriteBackupFunc(), deliver)
}

func (s *application) runBatchTasksWithBackup(
	ctx context.Context,
	tasks []batchTask,
	options compress.Options,
	outputMode int,
	outputDir string,
	usedPaths map[string]bool,
	backup func(string) error,
	deliver func(func()),
) {
	total := len(tasks)
	if total == 0 || ctx.Err() != nil {
		return
	}
	workers := batchWorkerCount(total)
	if options.Threads <= 0 {
		options.Threads = batchThreadsPerWorker(workers)
	}
	var destinations map[int]string
	var outputErrors map[int]error
	if outputMode == batchOutputFolder {
		options.SkipOverwriteSnapshot = true
		destinations, outputErrors = planBatchOutputs(ctx, tasks, outputDir, usedPaths)
	} else if options.OutputFormat == compress.OutputFormatWebP {
		var copyTasks []batchTask
		for _, task := range tasks {
			if !task.output.replacesFile(task.sourceFormat, task.outputFormat) {
				copyTasks = append(copyTasks, task)
			}
		}
		if len(copyTasks) > 0 {
			destinations, outputErrors = planBatchOutputs(ctx, copyTasks, outputDir, usedPaths)
		}
	}
	var deliverMu sync.Mutex
	emit := func(fn func()) {
		deliverMu.Lock()
		defer deliverMu.Unlock()
		deliver(fn)
	}
	process := func(ctx context.Context, task batchTask) batchCompressionOutcome {
		if err := outputErrors[task.index]; err != nil {
			return batchCompressionOutcome{err: err}
		}
		name := filepath.Base(task.path)
		started := false
		result, release, err := compress.FileWithAcquire(
			ctx,
			task.path,
			options,
			func(info compress.ImageInfo) (func(), error) {
				return s.imageMemory.acquire(ctx, imageMemoryEstimate(info, false))
			},
			func() {
				started = true
				emit(func() {
					if ctx.Err() != nil {
						return
					}
					if s.batch != nil && task.index < len(s.batch.items) {
						s.batch.items[task.index].status = batchProcessing
						s.batch.currentName = name
						if !s.canceling {
							s.phase = fmt.Sprintf("正在压缩 %d / %d · %s", min(total, s.batch.done+1), total, name)
						}
					}
				})
			},
		)
		return batchCompressionOutcome{result: result, err: err, started: started, release: release}
	}
	runBatchPipeline(ctx, tasks, workers, process, func(task batchTask, outcome batchCompressionOutcome) {
		if ctx.Err() != nil {
			if outcome.started {
				emit(func() {
					if s.batch != nil && task.index < len(s.batch.items) && s.batch.items[task.index].status == batchProcessing {
						s.batch.items[task.index].status = batchPending
					}
				})
			}
			return
		}
		update := saveBatchOutcomeWithBackup(task, outcome, outputMode, destinations[task.index], backup)
		emit(func() { s.applyBatchItemUpdate(update) })
	})
}

func saveBatchOutcome(task batchTask, outcome batchCompressionOutcome, outputMode int, destination string) batchItemUpdate {
	return saveBatchOutcomeWithBackup(task, outcome, outputMode, destination, nil)
}

func saveBatchOutcomeWithBackup(task batchTask, outcome batchCompressionOutcome, outputMode int, destination string, backup func(string) error) batchItemUpdate {
	update := batchItemUpdate{index: task.index, status: batchFailed, advanceDone: true}
	if outcome.err != nil {
		update.errMsg = outcome.err.Error()
		return update
	}
	result := outcome.result
	unchanged, outputSize := result.Unchanged, result.OutputSize
	saveAsCopy := outputMode == batchOutputFolder || destination != ""
	if saveAsCopy {
		err := os.MkdirAll(filepath.Dir(destination), 0o755)
		if err == nil {
			err = result.Save(destination)
		}
		if err != nil {
			update.errMsg = err.Error()
			return update
		}
		update.savedPath = destination
	} else if result.Unchanged || result.OutputSize >= result.OriginalSize {
		outputSize, unchanged = result.OriginalSize, true
	} else {
		if err := result.OverwriteWithBackup(backup); err != nil {
			update.errMsg = err.Error()
			return update
		}
		update.overwritten = true
	}
	update.format, update.width, update.height = result.Format, result.Width, result.Height
	update.originalSize, update.outputSize, update.unchanged = result.OriginalSize, outputSize, unchanged
	update.status = batchDone
	return update
}
