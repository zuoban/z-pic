package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

func printWatchCLIHelp(w io.Writer) {
	fmt.Fprintf(w, `用法: z-pic watch --output-dir <目录> [选项] <输入目录>

持续监听输入目录的顶层，自动压缩新到达的静态 %s 图片。
启动时跳过已有文件。输出目录须位于输入目录之外，原图和已有输出均保留。
文件大小和修改时间稳定约 %s 后尝试压缩，按 Ctrl+C 停止。

必需选项:
  --output-dir <目录>          保存副本的目录，缺少时自动创建目录

压缩选项（与 compress 相同）:
  -p, --preset <名称>          使用已保存的压缩预设
      --settings <路径>        指定设置 JSON 文件路径
  -q, --quality <1-100>        压缩质量
      --lossless              无损模式
      --format <%s>  输出格式
      --max-dim <px>          限制长边像素
      --max-width <px>        限制显示宽度
      --max-height <px>       限制显示高度
      --scale-percent <N>     等比缩放百分比
      --max-size <体积>        尝试压到目标体积以内，例如 '500 KiB'
      --target-resize-min <px> 超标时自动缩小的长边下限
      --metadata <策略>       keep-all | keep-icc | strip

运行 "z-pic help compress" 查看压缩选项的取值和格式说明。
`, strings.Join(supportedImageFormats(), " / "), watchSettleTime, cliOutputFormats)
}

func runCLIWatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var compressionFlags cliCompressionFlags
	compressionFlags.bind(flags)
	outputDir := flags.String("output-dir", "", "output directory")
	setFlags, err := parseCLIFlags(flags, args)
	if errors.Is(err, flag.ErrHelp) {
		printWatchCLIHelp(stdout)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	if flags.NArg() != 1 || flags.Arg(0) == "" || *outputDir == "" {
		fmt.Fprintln(stderr, "请提供一个输入目录及 --output-dir。\n运行 \"z-pic help watch\" 查看用法。")
		return 2
	}

	cfg, err := compressionFlags.loadSettings(setFlags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	options, _, err := compressionFlags.options(cfg, setFlags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	options.SkipOverwriteSnapshot = true
	if ctx.Err() != nil {
		return 0
	}

	input, err := openWatchDirectory(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "无法打开输入目录：%v\n", err)
		return 2
	}
	defer input.Close()
	resolvedOutput, err := resolveWatchOutputDir(ctx, *outputDir, input.identity)
	if ctx.Err() != nil {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "无效的输出目录：%v\n", err)
		return 2
	}
	if err := os.MkdirAll(resolvedOutput, 0o755); err != nil {
		fmt.Fprintf(stderr, "无法创建输出目录：%v\n", err)
		return 1
	}
	output, err := openWatchDirectory(resolvedOutput)
	if err != nil {
		fmt.Fprintf(stderr, "无法打开输出目录：%v\n", err)
		return 1
	}
	defer output.Close()
	if os.SameFile(input.identity, output.identity) {
		fmt.Fprintln(stderr, "输出目录必须位于输入目录之外。")
		return 2
	}

	watch := cliWatch{input: input, output: output, options: options, stdout: stdout, stderr: stderr}
	if err := watch.run(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "监听失败：%v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "已停止监听。")
	return 0
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

type cliWatch struct {
	input, output  watchDirectory
	options        compress.Options
	stdout, stderr io.Writer
	files          map[string]*watchImage
}

type watchImage struct {
	info        os.FileInfo
	stableSince time.Time
	handled     bool // Present at startup or already saved for this arrival.
	attempted   bool // Failed for the current observed file state.
}

func (watch *cliWatch) run(ctx context.Context) error {
	if _, err := watch.scan(ctx, true); err != nil {
		return err
	}
	fmt.Fprintf(watch.stdout, "READY  正在监听 %s -> %s（已跳过 %d 个已有文件，按 Ctrl+C 停止）\n",
		watch.input.Name(), watch.output.Name(), len(watch.files))
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

func (watch *cliWatch) scan(ctx context.Context, baseline bool) ([]string, error) {
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
				fmt.Fprintf(watch.stderr, "FAIL  %s  无法读取文件信息：%v\n", filepath.Join(watch.input.Name(), name), err)
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

func (watch *cliWatch) inputMatches(name string, expected os.FileInfo) bool {
	info, err := watch.input.Lstat(name)
	return err == nil && sameWatchMetadata(expected, info) && os.SameFile(expected, info)
}

func (watch *cliWatch) process(ctx context.Context, name string, image *watchImage) {
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
			fmt.Fprintf(watch.stderr, "FAIL  %s  %v\n", path, err)
		}
		return
	}

	image.handled = true
	status := "ok"
	if result.Unchanged {
		status = "unchanged"
	}
	printCLIFileResult(watch.stdout, cliFileReport{
		Input: path, Output: saved, Status: status,
		OriginalBytes: result.OriginalSize, OutputBytes: result.OutputSize,
		SavedPercent:  float64(result.OriginalSize-result.OutputSize) / float64(result.OriginalSize) * 100,
		TargetResized: result.TargetResized, TargetSizeMissed: result.TargetSizeMissed,
		Width: result.Width, Height: result.Height, OriginalWidth: source.Width, OriginalHeight: source.Height,
	})
}

func (watch *cliWatch) compressImage(ctx context.Context, name, path string, expected os.FileInfo) (*compress.Result, compress.ImageInfo, error) {
	data, err := readWatchImage(ctx, watch.input.Root, name, expected)
	if err != nil {
		return nil, compress.ImageInfo{}, err
	}
	source, err := compress.InspectBytes(ctx, data)
	if err != nil {
		return nil, compress.ImageInfo{}, err
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

func (watch *cliWatch) saveImage(ctx context.Context, name string, expected os.FileInfo, result *compress.Result) (string, error) {
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
