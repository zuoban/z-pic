package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zpic/internal/compress"
)

func TestFolderWatchConcurrencyBoundAndDeferredReady(t *testing.T) {
	root := t.TempDir()
	inDir := filepath.Join(root, "in")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const maxWorkers = 2
	const totalFiles = 5

	var active atomic.Int32
	var maxObserved atomic.Int32
	started := make(chan struct{}, totalFiles)
	releaseBatch := make(chan struct{})
	var releaseOnce sync.Once

	events := make(chan struct{}, 8)
	readyCh := make(chan struct{})
	outcomes := make(chan folderWatchOutcome, totalFiles*2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = runFolderWatch(ctx, folderWatchRequest{
			InputDir:    inDir,
			OutputDir:   outDir,
			Options:     defaultPresets()[0].compressionOptions(),
			Concurrency: maxWorkers,
			EventSource: func(context.Context, string, bool) (<-chan struct{}, func(), error) {
				return events, func() {}, nil
			},
			AcquireMemory: func(ctx context.Context, _ compress.ImageInfo) (func(), error) {
				cur := active.Add(1)
				for {
					prev := maxObserved.Load()
					if cur <= prev || maxObserved.CompareAndSwap(prev, cur) {
						break
					}
				}
				started <- struct{}{}
				select {
				case <-releaseBatch:
				case <-ctx.Done():
					active.Add(-1)
					return nil, ctx.Err()
				}
				return func() {
					active.Add(-1)
				}, nil
			},
		}, folderWatchNotifications{
			Ready: func(folderWatchReady) {
				close(readyCh)
			},
			Image: func(outcome folderWatchOutcome) {
				outcomes <- outcome
			},
		})
	}()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseBatch) })
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runFolderWatch did not exit after cancel")
		}
	})

	select {
	case <-readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch ready")
	}

	// Write more files than maxWorkers so some ready items are deferred.
	for i := range totalFiles {
		writeTestJPEG(t, filepath.Join(inDir, fmt.Sprintf("img-%d.jpg", i)), 120, 90, 95)
	}
	events <- struct{}{}

	// Wait for the pool to saturate at maxWorkers.
	for range maxWorkers {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for initial workers to start")
		}
	}

	// Trigger extra scan while workers are saturated; no additional worker should launch.
	events <- struct{}{}
	select {
	case <-started:
		t.Fatal("worker exceeded configured concurrency limit")
	case <-time.After(150 * time.Millisecond):
	}

	if got := maxObserved.Load(); got > maxWorkers {
		t.Fatalf("max concurrent workers = %d, want <= %d", got, maxWorkers)
	}

	// Release workers and verify all deferred files are eventually processed.
	releaseOnce.Do(func() { close(releaseBatch) })
	seen := make(map[string]bool, totalFiles)
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for len(seen) < totalFiles {
		select {
		case outcome := <-outcomes:
			if outcome.Err != nil {
				t.Fatalf("unexpected error for %s: %v", outcome.Input, outcome.Err)
			}
			seen[filepath.Base(outcome.Input)] = true
		case <-deadline.C:
			t.Fatalf("timed out waiting for all %d files; processed %d (%v)", totalFiles, len(seen), seen)
		}
	}

	if got := maxObserved.Load(); got > maxWorkers {
		t.Fatalf("max concurrent workers = %d, want <= %d", got, maxWorkers)
	}

	cancel()
	select {
	case <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled on shutdown, got %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runFolderWatch deadlocked on shutdown")
	}
}

func TestFolderWatchShutdownWhileWorkersInFlightNoDeadlock(t *testing.T) {
	root := t.TempDir()
	inDir := filepath.Join(root, "in")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := make(chan struct{}, 8)
	readyCh := make(chan struct{})
	inWorker := make(chan struct{}, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runFolderWatch(ctx, folderWatchRequest{
			InputDir:    inDir,
			OutputDir:   outDir,
			Options:     defaultPresets()[0].compressionOptions(),
			Concurrency: 1,
			EventSource: func(context.Context, string, bool) (<-chan struct{}, func(), error) {
				return events, func() {}, nil
			},
			AcquireMemory: func(wctx context.Context, _ compress.ImageInfo) (func(), error) {
				inWorker <- struct{}{}
				<-wctx.Done()
				return nil, wctx.Err()
			},
		}, folderWatchNotifications{
			Ready: func(folderWatchReady) {
				close(readyCh)
			},
		})
	}()

	select {
	case <-readyCh:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for watch ready")
	}

	writeTestJPEG(t, filepath.Join(inDir, "blocking.jpg"), 100, 80, 95)
	events <- struct{}{}

	select {
	case <-inWorker:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for worker to enter AcquireMemory")
	}

	// Move input directory to trigger a runScan error while worker is still blocked on workerCtx.
	// cancelWorkers() must run before wg.Wait() so runFolderWatch exits cleanly without deadlock.
	movedDir := filepath.Join(root, "in-moved")
	if err := os.Rename(inDir, movedDir); err != nil {
		cancel()
		t.Fatal(err)
	}
	events <- struct{}{}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after input directory moved away")
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("runFolderWatch deadlocked when exiting on scan error with active worker")
	}
}

func TestFolderWatchApplyProcessResultSkipsStaleVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jpg")
	writeTestJPEG(t, path, 80, 60, 90)
	v1Info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Replace with a brand-new file (V2, distinct inode and size).
	staged := filepath.Join(dir, "sample-v2.jpg")
	writeTestJPEG(t, staged, 160, 120, 95)
	if err := os.Rename(staged, path); err != nil {
		t.Fatal(err)
	}
	v2Info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(v1Info, v2Info) && sameWatchMetadata(v1Info, v2Info) {
		t.Fatal("expected V2 file info to differ from V1")
	}

	t.Run("stale success keeps V2 unhandled and still reports V1", func(t *testing.T) {
		var reported []folderWatchOutcome
		stableAt := time.Now().Add(-2 * time.Second)
		w := &folderWatch{
			files: map[string]*watchImage{
				"sample.jpg": {info: v2Info, stableSince: stableAt},
			},
			inFlight: map[string]bool{"sample.jpg": true},
			notify: folderWatchNotifications{
				Image: func(o folderWatchOutcome) { reported = append(reported, o) },
			},
		}

		w.applyProcessResult(watchProcessResult{
			relPath: "sample.jpg",
			info:    v1Info,
			outcome: folderWatchOutcome{Input: path, Output: "/tmp/out.jpg"},
		}, false)

		if w.inFlight["sample.jpg"] {
			t.Fatal("expected relPath to be cleared from inFlight")
		}
		img := w.files["sample.jpg"]
		if img.handled {
			t.Fatal("stale V1 completion must not mark V2 as handled")
		}
		if img.attempted {
			t.Fatal("stale V1 completion must not mark V2 as attempted")
		}
		if !img.stableSince.Equal(stableAt) {
			t.Fatal("stale V1 completion must not alter V2 stableSince")
		}
		if len(reported) != 1 || reported[0].Output != "/tmp/out.jpg" {
			t.Fatalf("expected V1 outcome to still be reported, got %+v", reported)
		}
	})

	t.Run("stale error keeps V2 unattempted and still reports V1 error", func(t *testing.T) {
		var reported []folderWatchOutcome
		w := &folderWatch{
			files: map[string]*watchImage{
				"sample.jpg": {info: v2Info, stableSince: time.Now()},
			},
			inFlight: map[string]bool{"sample.jpg": true},
			notify: folderWatchNotifications{
				Image: func(o folderWatchOutcome) { reported = append(reported, o) },
			},
		}

		w.applyProcessResult(watchProcessResult{
			relPath: "sample.jpg",
			info:    v1Info,
			outcome: folderWatchOutcome{Input: path, Err: errors.New("decode failed")},
		}, false)

		if w.inFlight["sample.jpg"] {
			t.Fatal("expected relPath to be cleared from inFlight")
		}
		if w.files["sample.jpg"].attempted {
			t.Fatal("stale V1 error must not mark V2 as attempted")
		}
		if len(reported) != 1 || reported[0].Err == nil {
			t.Fatalf("expected V1 error outcome to still be reported, got %+v", reported)
		}
	})

	t.Run("stale changed does not reset V2 stableSince", func(t *testing.T) {
		stableAt := time.Now().Add(-2 * time.Second)
		w := &folderWatch{
			files: map[string]*watchImage{
				"sample.jpg": {info: v2Info, stableSince: stableAt},
			},
			inFlight: map[string]bool{"sample.jpg": true},
		}

		w.applyProcessResult(watchProcessResult{
			relPath: "sample.jpg",
			info:    v1Info,
			changed: true,
		}, false)

		if w.inFlight["sample.jpg"] {
			t.Fatal("expected relPath to be cleared from inFlight")
		}
		if !w.files["sample.jpg"].stableSince.Equal(stableAt) {
			t.Fatal("stale V1 changed result must not reset V2 stableSince")
		}
	})

	t.Run("matching version updates state normally", func(t *testing.T) {
		var reported []folderWatchOutcome
		w := &folderWatch{
			files: map[string]*watchImage{
				"sample.jpg": {info: v2Info, stableSince: time.Now()},
			},
			inFlight: map[string]bool{"sample.jpg": true},
			notify: folderWatchNotifications{
				Image: func(o folderWatchOutcome) { reported = append(reported, o) },
			},
		}

		w.applyProcessResult(watchProcessResult{
			relPath: "sample.jpg",
			info:    v2Info,
			outcome: folderWatchOutcome{Input: path, Output: "/tmp/out-v2.jpg"},
		}, false)

		if !w.files["sample.jpg"].handled {
			t.Fatal("matching V2 completion must mark V2 as handled")
		}
		if len(reported) != 1 {
			t.Fatalf("expected V2 outcome to be reported, got %+v", reported)
		}
	})
}
