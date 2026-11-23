package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"zpic/internal/compress"
	"zpic/internal/imageinput"
)

const (
	watchPollInterval          = 250 * time.Millisecond
	watchEventFallbackInterval = 5 * time.Second
	watchSettleTime            = time.Second
)

var errWatchInputChanged = errors.New("监听文件已变更")

type folderWatchRequest struct {
	InputDir      string
	OutputDir     string
	Options       compress.Options
	Recursive     bool
	Concurrency   int
	AcquireMemory func(context.Context, compress.ImageInfo) (func(), error)
	EventSource   watchEventSource
}

type folderWatchReady struct {
	InputDir  string
	OutputDir string
	Skipped   int
}

type folderWatchOutcome struct {
	Input, Output                                string
	Err                                          error
	OriginalBytes, OutputBytes, TargetBytes      int64
	Unchanged, TargetResized, TargetSizeMissed   bool
	Width, Height, OriginalWidth, OriginalHeight int
}

// Notifications run synchronously on the caller's goroutine.
type folderWatchNotifications struct {
	Ready func(folderWatchReady)
	Image func(folderWatchOutcome)
}

type folderWatchPathError struct{ error }

func (e folderWatchPathError) Unwrap() error { return e.error }

func defaultWatchConcurrency() int {
	cpus := runtime.NumCPU()
	if cpus <= 1 {
		return 1
	}
	return min(max(cpus/2, 2), 4)
}

// runFolderWatch releases both directories and finishes all writes before returning.
func runFolderWatch(ctx context.Context, request folderWatchRequest, notify folderWatchNotifications) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.InputDir == "" || request.OutputDir == "" {
		return folderWatchPathError{errors.New("请提供输入目录和输出目录")}
	}
	input, err := openWatchDirectory(request.InputDir)
	if err != nil {
		return folderWatchPathError{fmt.Errorf("无法打开输入目录：%w", err)}
	}
	defer input.Close()
	resolvedOutput, err := resolveWatchOutputDir(ctx, request.OutputDir, input.Name(), input.identity, request.Recursive)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return folderWatchPathError{fmt.Errorf("无效的输出目录：%w", err)}
	}
	if err := os.MkdirAll(resolvedOutput, 0o755); err != nil {
		return fmt.Errorf("无法创建输出目录：%w", err)
	}
	output, err := openWatchDirectory(resolvedOutput)
	if err != nil {
		return fmt.Errorf("无法打开输出目录：%w", err)
	}
	defer output.Close()
	if os.SameFile(input.identity, output.identity) {
		return folderWatchPathError{errors.New("输出目录必须位于输入目录之外。")}
	}
	if request.Recursive && isWatchDirAncestor(output.identity, input.Name()) {
		return folderWatchPathError{errors.New("开启包含子目录时，输入目录不能位于输出目录之内。")}
	}

	concurrency := request.Concurrency
	if concurrency <= 0 {
		concurrency = defaultWatchConcurrency()
	}
	eventSource := request.EventSource
	if eventSource == nil {
		eventSource = defaultWatchEventSource
	}

	request.Options.SkipOverwriteSnapshot = true
	watch := folderWatch{
		input:         input,
		output:        output,
		options:       request.Options,
		recursive:     request.Recursive,
		concurrency:   concurrency,
		acquireMemory: request.AcquireMemory,
		eventSource:   eventSource,
		notify:        notify,
		inFlight:      make(map[string]bool),
	}
	if err := watch.run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("监听失败：%w", err)
	}
	return ctx.Err()
}

type watchDirectory struct {
	*os.Root
	identity os.FileInfo
}

func openWatchDirectory(dirPath string) (watchDirectory, error) {
	dirPath, err := filepath.Abs(dirPath)
	if err != nil {
		return watchDirectory{}, err
	}
	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return watchDirectory{}, err
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return watchDirectory{}, err
	}
	return watchDirectory{Root: root, identity: info}, nil
}

func (dir watchDirectory) checkPath() error {
	info, err := os.Stat(dir.Name())
	if err != nil {
		return err
	}
	if !os.SameFile(dir.identity, info) {
		return fmt.Errorf("目录位置已变更：%s", dir.Name())
	}
	return nil
}

func resolveWatchOutputDir(ctx context.Context, dirPath, inputDir string, inputInfo os.FileInfo, recursive bool) (string, error) {
	resolved, err := resolveOutputDirectory(ctx, dirPath)
	if err != nil {
		return "", err
	}
	// Compare directory identities to catch aliases, including when the
	// output itself does not exist yet but an ancestor aliases the input.
	for dir := resolved; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && os.SameFile(inputInfo, info) {
			return "", errors.New("输出目录必须位于输入目录之外")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if recursive {
		if outInfo, err := os.Stat(resolved); err == nil && isWatchDirAncestor(outInfo, inputDir) {
			return "", errors.New("开启包含子目录时，输入目录不能位于输出目录之内")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return resolved, nil
}

func isWatchDirAncestor(candidate os.FileInfo, targetPath string) bool {
	if candidate == nil || targetPath == "" {
		return false
	}
	checkPath := func(path string) bool {
		for dir := path; ; {
			parent := filepath.Dir(dir)
			if parent == dir {
				return false
			}
			dir = parent
			if info, err := os.Stat(dir); err == nil && os.SameFile(candidate, info) {
				return true
			}
		}
	}
	if checkPath(targetPath) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil && resolved != targetPath {
		return checkPath(resolved)
	}
	return false
}

type folderWatch struct {
	input, output watchDirectory
	options       compress.Options
	recursive     bool
	concurrency   int
	acquireMemory func(context.Context, compress.ImageInfo) (func(), error)
	eventSource   watchEventSource
	notify        folderWatchNotifications
	files         map[string]*watchImage
	inFlight      map[string]bool
}

type watchImage struct {
	info        os.FileInfo
	stableSince time.Time
	handled     bool // Present at startup or already saved for this arrival.
	attempted   bool // Failed for the current observed file state.
}

type watchProcessResult struct {
	relPath string
	info    os.FileInfo
	changed bool
	outcome folderWatchOutcome
}

func (watch *folderWatch) report(outcome folderWatchOutcome) {
	if watch.notify.Image != nil {
		watch.notify.Image(outcome)
	}
}

func (watch *folderWatch) applyProcessResult(res watchProcessResult, canceled bool) {
	delete(watch.inFlight, res.relPath)
	if canceled {
		return
	}
	img := watch.files[res.relPath]
	sameVersion := img != nil && img.info != nil && res.info != nil &&
		sameWatchMetadata(img.info, res.info) && os.SameFile(img.info, res.info)
	if res.changed {
		if sameVersion {
			img.stableSince = time.Time{}
		}
		return
	}
	if res.outcome.Err != nil {
		if sameVersion {
			img.attempted = true
		}
		watch.report(res.outcome)
		return
	}
	if sameVersion {
		img.handled = true
	}
	watch.report(res.outcome)
}

func (watch *folderWatch) run(ctx context.Context) error {
	var events <-chan struct{}
	var closeEvents func()
	if watch.eventSource != nil {
		ch, closer, err := watch.eventSource(ctx, watch.input.Name(), watch.recursive)
		if err == nil && ch != nil {
			events = ch
			closeEvents = closer
		}
	}
	if closeEvents != nil {
		defer closeEvents()
	}

	if _, _, err := watch.scan(ctx, true); err != nil {
		return err
	}
	if watch.notify.Ready != nil {
		watch.notify.Ready(folderWatchReady{
			InputDir:  watch.input.Name(),
			OutputDir: watch.output.Name(),
			Skipped:   len(watch.files),
		})
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	maxWorkers := max(1, watch.concurrency)
	results := make(chan watchProcessResult, maxWorkers)
	var wg sync.WaitGroup
	defer func() {
		cancelWorkers()
		wg.Wait()
	}()

	dispatchReady := func(ready []string) bool {
		for _, relPath := range ready {
			if workerCtx.Err() != nil {
				return false
			}
			img := watch.files[relPath]
			if img == nil || watch.inFlight[relPath] {
				continue
			}
			if len(watch.inFlight) >= maxWorkers {
				// Concurrency pool is full; remaining ready items will be picked up on the next cycle.
				return true
			}
			watch.inFlight[relPath] = true
			expected := img.info
			wg.Add(1)
			go func(rel string, info os.FileInfo) {
				defer wg.Done()
				res := watch.processFile(workerCtx, rel, info)
				select {
				case results <- res:
				case <-workerCtx.Done():
				}
			}(relPath, expected)
		}
		return false
	}

	applyResult := func(res watchProcessResult) {
		watch.applyProcessResult(res, workerCtx.Err() != nil)
	}

	pollInterval := watchPollInterval
	if events != nil {
		pollInterval = watchEventFallbackInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	settleTimer := time.NewTimer(time.Hour)
	if !settleTimer.Stop() {
		select {
		case <-settleTimer.C:
		default:
		}
	}
	defer settleTimer.Stop()
	var settleC <-chan time.Time

	armSettle := func(delay time.Duration) {
		if !settleTimer.Stop() {
			select {
			case <-settleTimer.C:
			default:
			}
		}
		if delay <= 0 {
			settleC = nil
			return
		}
		settleTimer.Reset(delay)
		settleC = settleTimer.C
	}

	runScan := func() error {
		ready, nextSettle, err := watch.scan(workerCtx, false)
		if err != nil {
			return err
		}
		if dispatchReady(ready) {
			// Some ready items waited for a worker slot; re-check soon.
			if nextSettle <= 0 || nextSettle > watchPollInterval {
				nextSettle = watchPollInterval
			}
		}
		if events != nil {
			armSettle(nextSettle)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			cancelWorkers()
			return ctx.Err()
		case res := <-results:
			applyResult(res)
			// Drain any additional completed results before rescanning.
		drain:
			for {
				select {
				case extra := <-results:
					applyResult(extra)
				default:
					break drain
				}
			}
			if err := runScan(); err != nil {
				cancelWorkers()
				return err
			}
		case _, ok := <-events:
			if !ok {
				// Native watcher closed; fall back to polling so directory changes are detected.
				events = nil
				ticker.Reset(watchPollInterval)
				armSettle(0)
			}
			if err := runScan(); err != nil {
				cancelWorkers()
				return err
			}
		case <-settleC:
			settleC = nil
			if err := runScan(); err != nil {
				cancelWorkers()
				return err
			}
		case <-ticker.C:
			if err := runScan(); err != nil {
				cancelWorkers()
				return err
			}
		}
	}
}

func (watch *folderWatch) scan(ctx context.Context, baseline bool) ([]string, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := watch.input.checkPath(); err != nil {
		return nil, 0, err
	}
	files := make(map[string]*watchImage, len(watch.files))
	var ready []string
	var nextSettle time.Duration

	inspectEntry := func(relPath string, d fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() && relPath != "." {
				if watch.recursive {
					return fs.SkipDir
				}
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || !isSupportedImageExt(name) {
			return nil
		}
		osRelPath := filepath.FromSlash(relPath)
		image := watch.files[relPath]
		info, err := watch.input.Lstat(osRelPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if image == nil || !image.stableSince.IsZero() {
				watch.report(folderWatchOutcome{
					Input: filepath.Join(watch.input.Name(), osRelPath),
					Err:   fmt.Errorf("无法读取文件信息：%w", err),
				})
			}
			if image == nil {
				image = &watchImage{handled: baseline}
			}
			image.stableSince = time.Time{}
			files[relPath] = image
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if image == nil || (image.info != nil && !os.SameFile(image.info, info)) {
			image = &watchImage{handled: baseline}
		}
		now := time.Now()
		if image.stableSince.IsZero() || !sameWatchMetadata(image.info, info) {
			image.stableSince, image.attempted = now, false
		}
		image.info = info
		files[relPath] = image
		if !image.handled && !image.attempted && !watch.inFlight[relPath] {
			elapsed := now.Sub(image.stableSince)
			if elapsed >= watchSettleTime {
				ready = append(ready, relPath)
			} else {
				remaining := watchSettleTime - elapsed + 20*time.Millisecond
				if nextSettle == 0 || remaining < nextSettle {
					nextSettle = remaining
				}
			}
		}
		return nil
	}

	if watch.recursive {
		err := fs.WalkDir(watch.input.FS(), ".", func(relPath string, d fs.DirEntry, err error) error {
			if err != nil {
				if relPath == "." {
					return err
				}
				return nil
			}
			if relPath == "." {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return inspectEntry(relPath, d)
		})
		if err != nil {
			return nil, 0, err
		}
	} else {
		entries, err := fs.ReadDir(watch.input.FS(), ".")
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			if err := inspectEntry(entry.Name(), entry); err != nil && !errors.Is(err, fs.SkipDir) {
				return nil, 0, err
			}
		}
	}

	watch.files = files
	return ready, nextSettle, nil
}

func sameWatchMetadata(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

func (watch *folderWatch) inputMatches(relPath string, expected os.FileInfo) bool {
	info, err := watch.input.Lstat(filepath.FromSlash(relPath))
	return err == nil && sameWatchMetadata(expected, info) && os.SameFile(expected, info)
}

func (watch *folderWatch) processFile(ctx context.Context, relPath string, expected os.FileInfo) watchProcessResult {
	osRelPath := filepath.FromSlash(relPath)
	fullPath := filepath.Join(watch.input.Name(), osRelPath)
	result, source, err := watch.compressImage(ctx, relPath, fullPath, expected)
	if ctx.Err() != nil {
		return watchProcessResult{relPath: relPath, info: expected}
	}
	if errors.Is(err, errWatchInputChanged) || !watch.inputMatches(relPath, expected) {
		return watchProcessResult{relPath: relPath, info: expected, changed: true}
	}
	var saved string
	if err == nil {
		saved, err = watch.saveImage(ctx, relPath, expected, result)
	}
	if errors.Is(err, errWatchInputChanged) {
		return watchProcessResult{relPath: relPath, info: expected, changed: true}
	}
	if err != nil {
		return watchProcessResult{
			relPath: relPath,
			info:    expected,
			outcome: folderWatchOutcome{Input: fullPath, Err: err},
		}
	}
	return watchProcessResult{
		relPath: relPath,
		info:    expected,
		outcome: folderWatchOutcome{
			Input: fullPath, Output: saved, Unchanged: result.Unchanged,
			OriginalBytes: result.OriginalSize, OutputBytes: result.OutputSize, TargetBytes: result.TargetSizeBytes,
			TargetResized: result.TargetResized, TargetSizeMissed: result.TargetSizeMissed,
			Width: result.Width, Height: result.Height, OriginalWidth: source.Width, OriginalHeight: source.Height,
		},
	}
}

func (watch *folderWatch) compressImage(ctx context.Context, relPath, fullPath string, expected os.FileInfo) (*compress.Result, compress.ImageInfo, error) {
	data, err := readWatchImage(ctx, watch.input.Root, filepath.FromSlash(relPath), expected)
	if err != nil {
		return nil, compress.ImageInfo{}, err
	}
	source, err := compress.InspectBytes(ctx, data)
	if err != nil {
		return nil, compress.ImageInfo{}, err
	}
	if watch.acquireMemory != nil {
		release, err := watch.acquireMemory(ctx, source)
		if err != nil {
			return nil, compress.ImageInfo{}, err
		}
		defer release()
	}
	result, err := compress.Bytes(ctx, data, fullPath, watch.options)
	return result, source, err
}

func readWatchImage(ctx context.Context, root *os.Root, osRelPath string, expected os.FileInfo) ([]byte, error) {
	file, err := root.Open(osRelPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !sameWatchMetadata(expected, info) || !os.SameFile(expected, info) {
		return nil, errWatchInputChanged
	}
	data, err := imageinput.ReadFile(ctx, file, expected.Size())
	if err == nil && int64(len(data)) != expected.Size() {
		return nil, errWatchInputChanged
	}
	return data, err
}

func mkdirAllInRoot(root *os.Root, relDir string) error {
	relDir = path.Clean(relDir)
	if relDir == "." || relDir == "" || relDir == "/" {
		return nil
	}
	parts := strings.Split(relDir, "/")
	current := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		if err := root.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			if info, statErr := root.Stat(current); statErr == nil && info.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
}

func (watch *folderWatch) saveImage(ctx context.Context, relPath string, expected os.FileInfo, result *compress.Result) (string, error) {
	if err := watch.output.checkPath(); err != nil {
		return "", err
	}
	relDir := path.Dir(relPath)
	baseName := path.Base(relPath)
	outputName := baseName
	if !expectedExtMatchesFormat(filepath.Ext(baseName), result.Format) {
		outputName = replaceFilenameWithConcreteFormat(baseName, "", result.Format)
	}
	targetDir := watch.output.Name()
	if relDir != "." && relDir != "" {
		if err := mkdirAllInRoot(watch.output.Root, relDir); err != nil {
			return "", fmt.Errorf("无法创建输出子目录：%w", err)
		}
		targetDir = filepath.Join(watch.output.Name(), filepath.FromSlash(relDir))
	}
	planner := batchOutputPlanner{ctx: ctx}
	for {
		destPath, err := planner.resolve(targetDir, result.SourcePath, outputName)
		if err != nil {
			return "", err
		}
		if !watch.inputMatches(relPath, expected) {
			return "", errWatchInputChanged
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rootRelName := filepath.Base(destPath)
		if relDir != "." && relDir != "" {
			rootRelName = filepath.Join(filepath.FromSlash(relDir), rootRelName)
		}
		err = result.SaveInRoot(watch.output.Root, rootRelName)
		if !errors.Is(err, os.ErrExist) {
			return destPath, err
		}
	}
}
