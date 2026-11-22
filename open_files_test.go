package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestEnqueueOpenFileCoalescesRapidBurstIntoSingleBatch(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 25)
	for i := range 25 {
		path := filepath.Join(root, fmt.Sprintf("photo-%02d.jpg", i))
		writeTestJPEG(t, path, 64, 48, 85)
		paths = append(paths, path)
	}

	app := newApplication()
	t.Cleanup(func() {
		app.closed = true
		app.cancelOpenFiles()
		if app.cancel != nil {
			app.cancel()
		}
		if app.thumbnails.cancel != nil {
			app.thumbnails.cancel()
		}
	})
	app.openFiles.delay = 20 * time.Millisecond
	updates := make(chan func(), 64)
	app.dispatchUpdate = func(update func()) { updates <- update }

	app.enqueueOpenFile("")
	for _, path := range paths {
		app.enqueueOpenFile(path)
	}
	app.enqueueOpenFile(paths[0])
	app.enqueueOpenFile(paths[len(paths)-1])

	select {
	case fn := <-updates:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coalesced open-file flush")
	}

	deadline := time.After(5 * time.Second)
	for app.batch == nil || len(app.batch.items) < len(paths) {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			got := 0
			if app.batch != nil {
				got = len(app.batch.items)
			}
			t.Fatalf("timed out waiting for batch load: got %d items, want %d (dropQueue=%d, dropFailed=%v, dropMessage=%q)",
				got, len(paths), len(app.dropQueue), app.dropFailed, app.dropMessage)
		}
	}
	if len(app.batch.items) != len(paths) {
		t.Fatalf("batch item count = %d, want %d", len(app.batch.items), len(paths))
	}
	if app.dropFailed || len(app.dropQueue) != 0 {
		t.Fatalf("expected clean coalesced import without drop queue overflow: dropFailed=%v dropMessage=%q dropQueue=%d",
			app.dropFailed, app.dropMessage, len(app.dropQueue))
	}
}

func TestEnqueueOpenFileWhileBusyQueuesSingleDropEntry(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 25)
	for i := range 25 {
		paths = append(paths, filepath.Join(root, fmt.Sprintf("queued-%02d.jpg", i)))
	}

	app := newApplication()
	t.Cleanup(app.cancelOpenFiles)
	app.openFiles.delay = 20 * time.Millisecond
	app.busy = true
	updates := make(chan func(), 16)
	app.dispatchUpdate = func(update func()) { updates <- update }

	for _, path := range paths {
		app.enqueueOpenFile(path)
	}

	select {
	case fn := <-updates:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for busy open-file flush")
	}

	if len(app.dropQueue) != 1 {
		t.Fatalf("dropQueue len = %d, want 1 coalesced entry", len(app.dropQueue))
	}
	if app.dropFailed {
		t.Fatalf("unexpected drop failure: %q", app.dropMessage)
	}
	select {
	case extra := <-updates:
		extra()
		t.Fatalf("expected only one coalesced update, got extra flush (dropQueue=%d)", len(app.dropQueue))
	case <-time.After(60 * time.Millisecond):
	}
}

func TestCancelOpenFilesStopsPendingAndFutureEnqueues(t *testing.T) {
	app := newApplication()
	app.openFiles.delay = 40 * time.Millisecond
	updates := make(chan func(), 4)
	app.dispatchUpdate = func(update func()) { updates <- update }

	app.enqueueOpenFile("/tmp/a.jpg")
	app.enqueueOpenFile("/tmp/b.jpg")
	app.cancelOpenFiles()
	app.enqueueOpenFile("/tmp/c.jpg")

	select {
	case <-updates:
		t.Fatal("expected canceled open-file coalescer not to flush")
	case <-time.After(120 * time.Millisecond):
	}
}
