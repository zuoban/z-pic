package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zpic/internal/compress"
	"zpic/internal/imageinput"
)

const (
	watchPollInterval = 250 * time.Millisecond
	watchSettleTime   = time.Second
)

var errWatchInputChanged = errors.New("监听文件已变更")

type folderWatchRequest struct {
	InputDir      string
	OutputDir     string
	Options       compress.Options
	AcquireMemory func(context.Context, compress.ImageInfo) (func(), error)
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
	resolvedOutput, err := resolveWatchOutputDir(ctx, request.OutputDir, input.identity)
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

	request.Options.SkipOverwriteSnapshot = true
	watch := folderWatch{
		input: input, output: output, options: request.Options,
		acquireMemory: request.AcquireMemory, notify: notify,
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

func openWatchDirectory(path string) (watchDirectory, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return watchDirectory{}, err
	}
	root, err := os.OpenRoot(path)
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

func resolveWatchOutputDir(ctx context.Context, path string, inputInfo os.FileInfo) (string, error) {
	resolved, err := resolveOutputDirectory(ctx, path)
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
			return resolved, nil
		}
	}
}

type folderWatch struct {
	input, output watchDirectory
	options       compress.Options
	acquireMemory func(context.Context, compress.ImageInfo) (func(), error)
	notify        folderWatchNotifications
	files         map[string]*watchImage
}

type watchImage struct {
	info        os.FileInfo
	stableSince time.Time
	handled     bool // Present at startup or already saved for this arrival.
	attempted   bool // Failed for the current observed file state.
}

func (watch *folderWatch) report(outcome folderWatchOutcome) {
	if watch.notify.Image != nil {
		watch.notify.Image(outcome)
	}
}

func (watch *folderWatch) run(ctx context.Context) error {
	if _, err := watch.scan(ctx, true); err != nil {
		return err
	}
	if watch.notify.Ready != nil {
		watch.notify.Ready(folderWatchReady{InputDir: watch.input.Name(), OutputDir: watch.output.Name(), Skipped: len(watch.files)})
	}
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ready, err := watch.scan(ctx, false)
			if err != nil {
				return err
			}
			for _, name := range ready {
				if err := ctx.Err(); err != nil {
					return err
				}
				watch.process(ctx, name, watch.files[name])
			}
		}
	}
}

func (watch *folderWatch) scan(ctx context.Context, baseline bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := watch.input.checkPath(); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(watch.input.FS(), ".")
	if err != nil {
		return nil, err
	}
	files := make(map[string]*watchImage, len(watch.files))
	var ready []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.Type().IsRegular() || !isSupportedImageExt(name) {
			continue
		}
		image := watch.files[name]
		info, err := watch.input.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if image == nil || !image.stableSince.IsZero() {
				watch.report(folderWatchOutcome{
					Input: filepath.Join(watch.input.Name(), name),
					Err:   fmt.Errorf("无法读取文件信息：%w", err),
				})
			}
			if image == nil {
				image = &watchImage{handled: baseline}
			}
			image.stableSince = time.Time{}
			files[name] = image
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if image == nil || (image.info != nil && !os.SameFile(image.info, info)) {
			image = &watchImage{handled: baseline}
		}
		now := time.Now()
		if image.stableSince.IsZero() || !sameWatchMetadata(image.info, info) {
			image.stableSince, image.attempted = now, false
		}
		image.info = info
		files[name] = image
		if !image.handled && !image.attempted && now.Sub(image.stableSince) >= watchSettleTime {
			ready = append(ready, name)
		}
	}
	watch.files = files
	return ready, nil
}

func sameWatchMetadata(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

func (watch *folderWatch) inputMatches(name string, expected os.FileInfo) bool {
	info, err := watch.input.Lstat(name)
	return err == nil && sameWatchMetadata(expected, info) && os.SameFile(expected, info)
}

func (watch *folderWatch) process(ctx context.Context, name string, image *watchImage) {
	path := filepath.Join(watch.input.Name(), name)
	result, source, err := watch.compressImage(ctx, name, path, image.info)
	if ctx.Err() != nil {
		return
	}
	if errors.Is(err, errWatchInputChanged) || !watch.inputMatches(name, image.info) {
		image.stableSince = time.Time{}
		return
	}
	var saved string
	if err == nil {
		saved, err = watch.saveImage(ctx, name, image.info, result)
	}
	if errors.Is(err, errWatchInputChanged) {
		image.stableSince = time.Time{}
		return
	}
	if err != nil {
		if ctx.Err() == nil {
			image.attempted = true
			watch.report(folderWatchOutcome{Input: path, Err: err})
		}
		return
	}

	image.handled = true
	watch.report(folderWatchOutcome{
		Input: path, Output: saved, Unchanged: result.Unchanged,
		OriginalBytes: result.OriginalSize, OutputBytes: result.OutputSize, TargetBytes: result.TargetSizeBytes,
		TargetResized: result.TargetResized, TargetSizeMissed: result.TargetSizeMissed,
		Width: result.Width, Height: result.Height, OriginalWidth: source.Width, OriginalHeight: source.Height,
	})
}

func (watch *folderWatch) compressImage(ctx context.Context, name, path string, expected os.FileInfo) (*compress.Result, compress.ImageInfo, error) {
	data, err := readWatchImage(ctx, watch.input.Root, name, expected)
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
	result, err := compress.Bytes(ctx, data, path, watch.options)
	return result, source, err
}

func readWatchImage(ctx context.Context, root *os.Root, name string, expected os.FileInfo) ([]byte, error) {
	file, err := root.Open(name)
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

func (watch *folderWatch) saveImage(ctx context.Context, name string, expected os.FileInfo, result *compress.Result) (string, error) {
	if err := watch.output.checkPath(); err != nil {
		return "", err
	}
	outputName := name
	if !expectedExtMatchesFormat(filepath.Ext(name), result.Format) {
		outputName = replaceFilenameWithConcreteFormat(name, "", result.Format)
	}
	planner := batchOutputPlanner{ctx: ctx}
	for {
		path, err := planner.resolve(watch.output.Name(), result.SourcePath, outputName)
		if err != nil {
			return "", err
		}
		if !watch.inputMatches(name, expected) {
			return "", errWatchInputChanged
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		err = result.SaveInRoot(watch.output.Root, filepath.Base(path))
		if !errors.Is(err, os.ErrExist) {
			return path, err
		}
	}
}
