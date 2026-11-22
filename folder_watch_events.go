package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

type watchEventSource func(ctx context.Context, inputDir string, recursive bool) (<-chan struct{}, func(), error)

func ignoreHiddenWatchPath(rel string, _ bool) bool {
	if rel == "" || rel == "." {
		return false
	}
	for segment := range strings.SplitSeq(rel, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

// defaultWatchEventSource uses MyGo's native OS file watcher (FSEvents on macOS,
// ReadDirectoryChangesW on Windows, inotify on Linux) when running inside the
// desktop app, and returns nil so headless CLI / tests fall back to polling.
func defaultWatchEventSource(ctx context.Context, inputDir string, recursive bool) (<-chan struct{}, func(), error) {
	if !mygo.App.IsReady() {
		return nil, nil, nil
	}
	watcher, err := mygo.WatchFiles(ctx, inputDir, mygo.FileWatchOptions{
		Recursive: recursive,
		HintsOnly: true,
		Debounce:  50 * time.Millisecond,
		MaxDelay:  250 * time.Millisecond,
		Ignore:    ignoreHiddenWatchPath,
	})
	if err != nil {
		return nil, nil, err
	}
	signals := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(signals)
		for {
			_, err := watcher.Next(ctx)
			select {
			case signals <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	closeFn := func() {
		_ = watcher.Close()
		<-done
	}
	return signals, closeFn, nil
}
