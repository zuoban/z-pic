package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func printWatchCLIHelp(w io.Writer) {
	fmt.Fprintf(w, `用法: z-pic watch --output-dir <目录> [选项] <输入目录>

持续监听输入目录，自动压缩新到达的静态 %s 图片。
启动时跳过已有文件。输出目录须位于输入目录之外，原图和已有输出均保留。
文件大小和修改时间稳定约 %s 后尝试压缩，按 Ctrl+C 停止。

必需选项:
  --output-dir <目录>          保存副本的目录，缺少时自动创建目录

监听选项:
  -r, --recursive             递归监听子目录，并在输出目录中保留相对子目录结构
  -j, --jobs <N>              并发压缩工作数（默认自动）
      --json                  以 NDJSON（每行一条 JSON 事件）输出

压缩选项（与 compress 相同）:
  -p, --preset <名称>          使用已保存的压缩预设
      --settings <路径>        指定设置 JSON 文件路径
  -q, --quality <1-100>        压缩质量
      --lossless              无损模式
      --format <%s>  输出格式
      --jpeg-bg <颜色>        透明图转 JPEG 底色（white | black | #RRGGBB）
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

type cliWatchEvent struct {
	Event     string         `json:"event"`
	InputDir  string         `json:"input_dir,omitempty"`
	OutputDir string         `json:"output_dir,omitempty"`
	Recursive bool           `json:"recursive,omitempty"`
	Skipped   int            `json:"skipped,omitempty"`
	File      *cliFileReport `json:"file,omitempty"`
}

func runCLIWatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var compressionFlags cliCompressionFlags
	compressionFlags.bind(flags)
	outputDir := flags.String("output-dir", "", "output directory")
	var (
		recursive bool
		jobs      int
		jsonOut   bool
	)
	flags.BoolVar(&recursive, "recursive", false, "watch subdirectories recursively")
	flags.BoolVar(&recursive, "r", false, "watch subdirectories recursively")
	flags.IntVar(&jobs, "jobs", 0, "worker concurrency")
	flags.IntVar(&jobs, "j", 0, "worker concurrency")
	flags.BoolVar(&jsonOut, "json", false, "output NDJSON events")

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
	if jobs < 0 {
		fmt.Fprintln(stderr, "参数错误：--jobs 不能为负数。")
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
	if ctx.Err() != nil {
		return 0
	}
	request := folderWatchRequest{
		InputDir:    flags.Arg(0),
		OutputDir:   *outputDir,
		Options:     options,
		Recursive:   recursive,
		Concurrency: jobs,
	}
	emitJSON := func(event cliWatchEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		_, _ = stdout.Write(append(data, '\n'))
	}
	notify := folderWatchNotifications{
		Ready: func(ready folderWatchReady) {
			if jsonOut {
				emitJSON(cliWatchEvent{
					Event:     "ready",
					InputDir:  ready.InputDir,
					OutputDir: ready.OutputDir,
					Recursive: recursive,
					Skipped:   ready.Skipped,
				})
				return
			}
			scope := "顶层"
			if recursive {
				scope = "含子目录"
			}
			fmt.Fprintf(stdout, "READY  正在监听 %s（%s） -> %s（已跳过 %d 个已有文件，按 Ctrl+C 停止）\n",
				ready.InputDir, scope, ready.OutputDir, ready.Skipped)
		},
		Image: func(outcome folderWatchOutcome) {
			if jsonOut {
				report := cliWatchOutcomeReport(outcome)
				emitJSON(cliWatchEvent{Event: "file", File: &report})
				return
			}
			printCLIWatchOutcome(stdout, stderr, outcome)
		},
	}
	if err := runFolderWatch(ctx, request, notify); err != nil && ctx.Err() == nil {
		fmt.Fprintln(stderr, err)
		var pathError folderWatchPathError
		if errors.As(err, &pathError) {
			return 2
		}
		return 1
	}
	if jsonOut {
		emitJSON(cliWatchEvent{Event: "stopped"})
		return 0
	}
	fmt.Fprintln(stdout, "已停止监听。")
	return 0
}

func cliWatchOutcomeReport(outcome folderWatchOutcome) cliFileReport {
	if outcome.Err != nil {
		return cliFileReport{
			Input:  outcome.Input,
			Status: "failed",
			Error:  outcome.Err.Error(),
		}
	}
	status := "ok"
	if outcome.Unchanged {
		status = "unchanged"
	}
	var savedPercent float64
	if outcome.OriginalBytes > 0 {
		savedPercent = float64(outcome.OriginalBytes-outcome.OutputBytes) / float64(outcome.OriginalBytes) * 100
	}
	return cliFileReport{
		Input:            outcome.Input,
		Output:           outcome.Output,
		Status:           status,
		OriginalBytes:    outcome.OriginalBytes,
		OutputBytes:      outcome.OutputBytes,
		SavedPercent:     savedPercent,
		TargetResized:    outcome.TargetResized,
		TargetSizeMissed: outcome.TargetSizeMissed,
		Width:            outcome.Width,
		Height:           outcome.Height,
		OriginalWidth:    outcome.OriginalWidth,
		OriginalHeight:   outcome.OriginalHeight,
	}
}

func printCLIWatchOutcome(stdout, stderr io.Writer, outcome folderWatchOutcome) {
	if outcome.Err != nil {
		fmt.Fprintf(stderr, "FAIL  %s  %v\n", outcome.Input, outcome.Err)
		return
	}
	printCLIFileResult(stdout, cliWatchOutcomeReport(outcome))
}
