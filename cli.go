package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"

	"zpic/internal/compress"
)

//go:embed mygo.json
var mygoConfigJSON []byte

var appVersion = loadAppVersion()
var cliTrashItem = mygo.Shell.TrashItem

func loadAppVersion() string {
	var config struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(mygoConfigJSON, &config); err != nil || config.Version == "" {
		return "0.1.0"
	}
	return config.Version
}

func shouldRunCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "compress", "presets", "version", "help", "-h", "--help", "-v", "--version":
		return true
	default:
		return false
	}
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runCLIContext(ctx, args, stdout, stderr)
}

func runCLIContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRootCLIHelp(stdout)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		return runCLIHelp(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		return runCLIVersion(args[1:], stdout, stderr)
	case "presets":
		return runCLIPresets(args[1:], stdout, stderr)
	case "compress":
		return runCLICompress(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n运行 \"z-pic help\" 查看可用命令。\n", args[0])
		return 2
	}
}

func printRootCLIHelp(w io.Writer) {
	fmt.Fprintf(w, `Z Pic %s — 本地静态图片压缩工具（JPEG / PNG / WebP）

用法:
  z-pic [图片或文件夹 ...]           启动桌面界面（可选直接加载文件）
  z-pic compress [选项] <路径 ...>   无界面批量压缩图片或文件夹
  z-pic presets [选项]               列出已保存的压缩预设
  z-pic version [--json]             显示版本信息
  z-pic help [命令]                  显示帮助信息

常用示例:
  z-pic compress hero.png
  z-pic compress -o ./dist --format webp --max-dim 1920 ./photos
  z-pic compress --preset 均衡 --in-place ./images
  z-pic compress --dry-run --json ./photos

运行 "z-pic help <命令>" 查看具体选项。
`, appVersion)
}

func runCLIHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRootCLIHelp(stdout)
		return 0
	}
	switch args[0] {
	case "compress":
		printCompressCLIHelp(stdout)
		return 0
	case "presets":
		printPresetsCLIHelp(stdout)
		return 0
	case "version":
		fmt.Fprintln(stdout, "用法: z-pic version [--json]\n\n显示 Z Pic 版本与运行平台信息。")
		return 0
	default:
		fmt.Fprintf(stderr, "未知帮助主题：%s\n", args[0])
		return 2
	}
}

type cliVersionReport struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func runCLIVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "用法: z-pic version [--json]")
			return 0
		}
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "version 命令不接受位置参数：%s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	report := cliVersionReport{
		Name:    "z-pic",
		Version: appVersion,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
	if *jsonOut {
		return writeCLIJSON(stdout, stderr, report)
	}
	fmt.Fprintf(stdout, "%s %s (%s/%s)\n", report.Name, report.Version, report.OS, report.Arch)
	return 0
}

func printPresetsCLIHelp(w io.Writer) {
	fmt.Fprintln(w, `用法: z-pic presets [选项]

列出当前配置文件中的压缩预设（配置文件不存在时显示内置默认预设）。

选项:
  --settings <path>   指定设置 JSON 文件路径（默认使用应用设置目录）
  --json              以 JSON 格式输出`)
}

type cliPNGOptions struct {
	MinQuality int `json:"min_quality"`
	MaxQuality int `json:"max_quality"`
	Colors     int `json:"colors"`
	Speed      int `json:"speed"`
}

type cliPresetEntry struct {
	Name         string        `json:"name"`
	Active       bool          `json:"active"`
	Quality      int           `json:"quality"`
	Lossless     bool          `json:"lossless"`
	Format       string        `json:"format"`
	MaxDimension int           `json:"max_dimension"`
	Metadata     string        `json:"metadata"`
	PNG          cliPNGOptions `json:"png"`
	SaveMode     string        `json:"save_mode"`
}

type cliPresetsReport struct {
	Selected     string           `json:"selected"`
	SettingsPath string           `json:"settings_path,omitempty"`
	Presets      []cliPresetEntry `json:"presets"`
}

func loadCLISettings(overridePath string) (appSettings, string, error) {
	path := overridePath
	if path == "" {
		resolved, err := settingsFilePath()
		if err != nil {
			return appSettings{
				presets:  defaultPresets(),
				selected: defaultPresets()[0].name,
				theme:    themeSystem,
			}, "", nil
		}
		path = resolved
	}
	cfg, err := loadFullSettings(path)
	if err != nil {
		return appSettings{}, path, err
	}
	return cfg, path, nil
}

func cliSaveModeName(mode presetSaveMode) string {
	switch mode {
	case presetSaveAsk:
		return "ask"
	case presetSaveFixed:
		return "fixed"
	default:
		return "original"
	}
}

func cliMetadataName(policy compress.MetadataPolicy) string {
	switch policy {
	case compress.MetadataKeepICC:
		return "keep-icc"
	case compress.MetadataStrip:
		return "strip"
	default:
		return "keep-all"
	}
}

func cliFormatName(format compress.OutputFormat) string {
	if format == compress.OutputFormatWebP {
		return "webp"
	}
	return "keep"
}

func runCLIPresets(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("presets", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	settingsPath := fs.String("settings", "", "settings file path")
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPresetsCLIHelp(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "presets 命令不接受位置参数：%s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	cfg, path, err := loadCLISettings(*settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "读取预设失败：%v\n", err)
		return 1
	}
	report := cliPresetsReport{
		Selected:     cfg.selected,
		SettingsPath: path,
		Presets:      make([]cliPresetEntry, 0, len(cfg.presets)),
	}
	for _, preset := range cfg.presets {
		report.Presets = append(report.Presets, cliPresetEntry{
			Name:         preset.name,
			Active:       preset.name == cfg.selected,
			Quality:      int(preset.quality),
			Lossless:     preset.lossless,
			Format:       cliFormatName(preset.outputFormat),
			MaxDimension: preset.maxDimension,
			Metadata:     cliMetadataName(preset.metadata),
			PNG: cliPNGOptions{
				MinQuality: preset.png.MinQuality,
				MaxQuality: preset.png.MaxQuality,
				Colors:     preset.png.Colors,
				Speed:      preset.png.Speed,
			},
			SaveMode: cliSaveModeName(preset.output.Mode),
		})
	}
	if *jsonOut {
		return writeCLIJSON(stdout, stderr, report)
	}
	for _, entry := range report.Presets {
		marker := "  "
		if entry.Active {
			marker = "* "
		}
		mode := fmt.Sprintf("质量 %d", entry.Quality)
		if entry.Lossless {
			mode = "无损"
		}
		extras := []string{mode}
		if entry.Format == "webp" {
			extras = append(extras, "转为 WebP")
		}
		if entry.MaxDimension > 0 {
			extras = append(extras, fmt.Sprintf("长边 ≤ %d px", entry.MaxDimension))
		}
		if entry.Metadata != "keep-all" {
			extras = append(extras, "元数据:"+entry.Metadata)
		}
		fmt.Fprintf(stdout, "%s%s  (%s)\n", marker, entry.Name, strings.Join(extras, " · "))
	}
	return 0
}

func printCompressCLIHelp(w io.Writer) {
	fmt.Fprintf(w, `用法: z-pic compress [选项] <图片或文件夹 ...>

无界面批量压缩静态 JPEG / PNG / WebP 图片（默认递归扫描目录）。
默认不会覆盖原文件，会在原目录生成带 "-zpic" 后缀的防冲突副本。

选项:
  -p, --preset <name>      使用已保存预设作为基础参数
      --settings <path>    指定设置 JSON 文件路径
  -q, --quality <1-100>    JPEG / WebP 质量并联动调整 PNG 量化参数
      --lossless           无损模式（PNG 仅 Oxipng，WebP 无损，JPEG 不二次压缩）
      --format <keep|webp> 输出格式（keep 保持原格式，webp 转为 WebP）
      --max-dim <px>       限制长边像素上限（0 表示原尺寸，范围 %d–%d）
      --metadata <policy>  元数据策略：keep-all | keep-icc | strip
  -o, --output <path>      输出文件路径（单图）或输出目录（批量/多图）
      --in-place           仅在体积减小时覆盖同格式原图（转 WebP 时另存为 .webp）
      --trash              覆盖前将原图移入系统废纸篓（仅与 --in-place 一起使用）
      --suffix <text>      副本文件名后缀（默认 -zpic）
      --no-recursive       只扫描指定目录的顶层
      --dry-run            仅在内存中试算压缩结果，不写入磁盘
  -j, --jobs <N>           并发任务数（0 表示自动）
      --json               以 JSON 格式输出完整报告
      --quiet              静默模式（仅输出错误）
`, compress.MinResizeDimension, compress.MaxResizeDimension)
}

type cliFileReport struct {
	Input         string  `json:"input"`
	Output        string  `json:"output,omitempty"`
	Status        string  `json:"status"`
	SourceFormat  string  `json:"source_format,omitempty"`
	OutputFormat  string  `json:"output_format,omitempty"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	OriginalBytes int64   `json:"original_bytes"`
	OutputBytes   int64   `json:"output_bytes"`
	SavedBytes    int64   `json:"saved_bytes"`
	SavedPercent  float64 `json:"saved_percent"`
	Unchanged     bool    `json:"unchanged,omitempty"`
	Overwritten   bool    `json:"overwritten,omitempty"`
	BackedUp      bool    `json:"backed_up,omitempty"`
	Error         string  `json:"error,omitempty"`
}

type cliCompressReport struct {
	DryRun        bool            `json:"dry_run"`
	InPlace       bool            `json:"in_place"`
	Preset        string          `json:"preset,omitempty"`
	Total         int             `json:"total"`
	Succeeded     int             `json:"succeeded"`
	Failed        int             `json:"failed"`
	Unchanged     int             `json:"unchanged"`
	Truncated     bool            `json:"truncated,omitempty"`
	OriginalBytes int64           `json:"original_bytes"`
	OutputBytes   int64           `json:"output_bytes"`
	SavedBytes    int64           `json:"saved_bytes"`
	SavedPercent  float64         `json:"saved_percent"`
	Files         []cliFileReport `json:"files"`
}

func parseCLIMetadataPolicy(raw string) (compress.MetadataPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "keep-all", "keep_all", "all":
		return compress.MetadataKeepAll, nil
	case "keep-icc", "keep_icc", "icc":
		return compress.MetadataKeepICC, nil
	case "strip", "none":
		return compress.MetadataStrip, nil
	default:
		return "", fmt.Errorf("无效的元数据策略 %q（支持 keep-all / keep-icc / strip）", raw)
	}
}

func parseCLIOutputFormat(raw string) (compress.OutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "keep":
		return compress.OutputFormatKeep, nil
	case "webp":
		return compress.OutputFormatWebP, nil
	default:
		return "", fmt.Errorf("无效的输出格式 %q（支持 keep / webp）", raw)
	}
}

func cliPNGOptionsForQuality(quality int) compress.PNGOptions {
	minQ := max(0, quality-20)
	maxQ := min(100, quality)
	colors := 256
	speed := 3
	switch {
	case quality <= 45:
		colors, speed = 64, 5
	case quality <= 65:
		colors, speed = 128, 3
	case quality <= 75:
		colors, speed = 192, 3
	case quality >= 90:
		minQ = max(85, quality-8)
		maxQ = 100
		colors, speed = 256, 1
	}
	return compress.PNGOptions{MinQuality: minQ, MaxQuality: maxQ, Colors: colors, Speed: speed}
}

func expectedExtMatchesFormat(ext, format string) bool {
	ext = strings.ToLower(ext)
	switch format {
	case "jpeg":
		return ext == ".jpg" || ext == ".jpeg"
	case "png":
		return ext == ".png"
	case "webp":
		return ext == ".webp"
	default:
		return false
	}
}

func runCLICompress(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compress", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		presetName   string
		settingsPath string
		quality      int
		lossless     bool
		formatFlag   string
		maxDim       int
		metadataFlag string
		outputPath   string
		inPlace      bool
		useTrash     bool
		suffix       string
		noRecursive  bool
		dryRun       bool
		jobs         int
		jsonOut      bool
		quiet        bool
	)
	fs.StringVar(&presetName, "preset", "", "preset name")
	fs.StringVar(&presetName, "p", "", "preset name")
	fs.StringVar(&settingsPath, "settings", "", "settings file path")
	fs.IntVar(&quality, "quality", 80, "quality 1-100")
	fs.IntVar(&quality, "q", 80, "quality 1-100")
	fs.BoolVar(&lossless, "lossless", false, "lossless compression")
	fs.StringVar(&formatFlag, "format", "", "output format: keep|webp")
	fs.IntVar(&maxDim, "max-dim", 0, "max long-edge dimension in pixels")
	fs.StringVar(&metadataFlag, "metadata", "", "metadata policy: keep-all|keep-icc|strip")
	fs.StringVar(&outputPath, "output", "", "output file or directory")
	fs.StringVar(&outputPath, "o", "", "output file or directory")
	fs.BoolVar(&inPlace, "in-place", false, "overwrite source files in place when smaller")
	fs.BoolVar(&useTrash, "trash", false, "move source files to the Trash before overwriting")
	fs.StringVar(&suffix, "suffix", "", "copy filename suffix")
	fs.BoolVar(&noRecursive, "no-recursive", false, "scan only the top level of input directories")
	fs.BoolVar(&dryRun, "dry-run", false, "estimate compression without writing files")
	fs.IntVar(&jobs, "jobs", 0, "worker concurrency")
	fs.IntVar(&jobs, "j", 0, "worker concurrency")
	fs.BoolVar(&jsonOut, "json", false, "JSON report")
	fs.BoolVar(&quiet, "quiet", false, "suppress normal output")

	reordered := reorderCLIFlags(args, map[string]bool{
		"p": true, "preset": true, "settings": true,
		"q": true, "quality": true, "format": true,
		"max-dim": true, "metadata": true,
		"o": true, "output": true, "suffix": true,
		"j": true, "jobs": true,
	})
	if err := fs.Parse(reordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCompressCLIHelp(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	if useTrash && !inPlace {
		fmt.Fprintln(stderr, "参数冲突：--trash 仅可与 --in-place 一起使用。")
		return 2
	}
	inputs := fs.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(stderr, "请提供至少一个图片文件或文件夹。\n运行 \"z-pic help compress\" 查看用法。")
		return 2
	}
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	if inPlace && outputPath != "" {
		fmt.Fprintln(stderr, "参数冲突：--in-place 与 --output 不能同时使用。")
		return 2
	}
	if inPlace && setFlags["suffix"] {
		fmt.Fprintln(stderr, "参数冲突：--in-place 与 --suffix 不能同时使用。")
		return 2
	}
	if jobs < 0 {
		fmt.Fprintln(stderr, "参数错误：--jobs 不能为负数。")
		return 2
	}
	if setFlags["suffix"] && suffix != "" && !validOutputComponent(suffix) {
		fmt.Fprintln(stderr, "参数错误：--suffix 不能包含路径分隔符或控制字符。")
		return 2
	}

	var cfg appSettings
	if presetName != "" || setFlags["settings"] {
		var err error
		cfg, _, err = loadCLISettings(settingsPath)
		if err != nil {
			fmt.Fprintf(stderr, "读取设置失败：%v\n", err)
			return 2
		}
		if inPlace && !setFlags["trash"] {
			useTrash = cfg.backupToTrash
		}
	}

	opts := compress.Options{
		Quality: 80,
		PNG:     compress.DefaultPNGOptions(),
	}
	var basePreset *compressionPreset
	if presetName != "" {
		for i := range cfg.presets {
			if cfg.presets[i].name == presetName {
				p := cfg.presets[i]
				basePreset = &p
				break
			}
		}
		if basePreset == nil {
			fmt.Fprintf(stderr, "找不到预设 %q；运行 \"z-pic presets\" 查看可用预设。\n", presetName)
			return 2
		}
		opts = compress.Options{
			Quality:      int(basePreset.quality),
			PNG:          basePreset.png,
			Lossless:     basePreset.lossless,
			Metadata:     basePreset.metadata,
			OutputFormat: basePreset.outputFormat,
			MaxDimension: basePreset.maxDimension,
		}
	}

	if setFlags["quality"] || setFlags["q"] {
		if quality < 1 || quality > 100 {
			fmt.Fprintln(stderr, "参数错误：--quality 必须在 1 到 100 之间。")
			return 2
		}
		opts.Quality = quality
		opts.PNG = cliPNGOptionsForQuality(quality)
	}
	if setFlags["lossless"] {
		opts.Lossless = lossless
	}
	if setFlags["format"] {
		parsed, err := parseCLIOutputFormat(formatFlag)
		if err != nil {
			fmt.Fprintf(stderr, "参数错误：%v\n", err)
			return 2
		}
		opts.OutputFormat = parsed
	}
	if setFlags["max-dim"] {
		if err := compress.ValidateMaxDimension(maxDim); err != nil {
			fmt.Fprintf(stderr, "参数错误：%v\n", err)
			return 2
		}
		opts.MaxDimension = maxDim
	}
	if setFlags["metadata"] {
		parsed, err := parseCLIMetadataPolicy(metadataFlag)
		if err != nil {
			fmt.Fprintf(stderr, "参数错误：%v\n", err)
			return 2
		}
		opts.Metadata = parsed
	}

	scan, err := scanBatchPathsWithOptions(ctx, inputs, nil, nil, 0, !noRecursive)
	if err != nil {
		fmt.Fprintf(stderr, "扫描输入失败：%v\n", err)
		return 1
	}
	if len(scan.items) == 0 {
		fmt.Fprintln(stderr, "未找到可处理的 JPEG、PNG 或 WebP 图片。")
		return 2
	}

	// Determine output strategy.
	singleExplicitFile := ""
	outputDir := ""
	saveOpts := presetSaveOptions{
		Mode:      presetSaveOriginal,
		UseSuffix: true,
		Suffix:    defaultFilenameSuffix,
	}
	if setFlags["suffix"] {
		saveOpts.UseSuffix = suffix != ""
		saveOpts.Suffix = suffix
	}
	if !dryRun && !inPlace {
		if outputPath != "" {
			info, statErr := os.Stat(outputPath)
			isExistingDir := statErr == nil && info.IsDir()
			singleInputIsFile := len(inputs) == 1 && len(scan.items) == 1 && scan.items[0].sourceDir == ""
			if singleInputIsFile && !isExistingDir && isSupportedImageExt(outputPath) {
				item := scan.items[0]
				if !item.invalidInput {
					effectiveFmt := item.format
					if opts.OutputFormat == compress.OutputFormatWebP {
						effectiveFmt = "webp"
					}
					if !expectedExtMatchesFormat(filepath.Ext(outputPath), effectiveFmt) {
						fmt.Fprintf(stderr, "参数错误：输出文件扩展名 %q 与目标格式 %s 不匹配。\n", filepath.Ext(outputPath), strings.ToUpper(effectiveFmt))
						return 2
					}
				}
				singleExplicitFile = outputPath
			} else {
				absOut, err := filepath.Abs(outputPath)
				if err != nil {
					fmt.Fprintf(stderr, "无效的输出目录：%v\n", err)
					return 2
				}
				if err := os.MkdirAll(absOut, 0o755); err != nil {
					fmt.Fprintf(stderr, "无法创建输出目录：%v\n", err)
					return 1
				}
				outputDir = absOut
				saveOpts = presetSaveOptions{
					Mode:      presetSaveFixed,
					Directory: absOut,
					UseSuffix: setFlags["suffix"] && suffix != "",
					Suffix:    suffix,
				}
			}
		} else if basePreset != nil && !setFlags["suffix"] &&
			!basePreset.output.replacesOriginal() && basePreset.output.Mode != presetSaveAsk {
			saveOpts = basePreset.output
			if saveOpts.Mode == presetSaveFixed && saveOpts.Directory != "" {
				if err := os.MkdirAll(saveOpts.Directory, 0o755); err != nil {
					fmt.Fprintf(stderr, "无法创建预设输出目录：%v\n", err)
					return 1
				}
				outputDir = saveOpts.Directory
			}
		}
	} else if inPlace {
		saveOpts = presetSaveOptions{Mode: presetSaveOriginal}
	}

	files := make([]cliFileReport, len(scan.items))
	tasks := make([]batchTask, 0, len(scan.items))
	for i, item := range scan.items {
		if item.invalidInput {
			files[i] = cliFileReport{
				Input:  item.path,
				Status: "failed",
				Error:  item.errMsg,
			}
			continue
		}
		tasks = append(tasks, batchTask{
			index:        i,
			path:         item.path,
			relPath:      item.relPath,
			sourceFormat: item.format,
			outputFormat: opts.OutputFormat,
			output:       saveOpts,
		})
	}

	destinations := make(map[int]string, len(tasks))
	planErrors := make(map[int]error)
	if !dryRun {
		if singleExplicitFile != "" && len(tasks) == 1 {
			destinations[tasks[0].index] = singleExplicitFile
		} else if !inPlace {
			opts.SkipOverwriteSnapshot = true
			destinations, planErrors = planBatchOutputs(ctx, tasks, outputDir, nil)
		} else if opts.OutputFormat == compress.OutputFormatWebP {
			var copyTasks []batchTask
			for _, task := range tasks {
				if !task.output.replacesFile(task.sourceFormat, task.outputFormat) {
					copyTasks = append(copyTasks, task)
				}
			}
			if len(copyTasks) > 0 {
				destinations, planErrors = planBatchOutputs(ctx, copyTasks, "", nil)
			}
		}
	} else {
		opts.SkipOverwriteSnapshot = true
	}

	workers := jobs
	if workers <= 0 {
		workers = batchWorkerCount(len(tasks))
	} else {
		workers = min(len(tasks), workers)
	}
	if opts.Threads <= 0 {
		opts.Threads = batchThreadsPerWorker(workers)
	}

	budget := newImageMemoryBudget(imageMemoryLimit)
	process := func(ctx context.Context, task batchTask) batchCompressionOutcome {
		if err, failed := planErrors[task.index]; failed {
			return batchCompressionOutcome{err: err}
		}
		started := false
		result, release, err := compress.FileWithAcquire(
			ctx,
			task.path,
			opts,
			func(info compress.ImageInfo) (func(), error) {
				return budget.acquire(ctx, imageMemoryEstimate(info, false))
			},
			func() { started = true },
		)
		return batchCompressionOutcome{result: result, err: err, started: started, release: release}
	}

	consume := func(task batchTask, outcome batchCompressionOutcome) {
		entry := cliFileReport{
			Input:         task.path,
			SourceFormat:  task.sourceFormat,
			OriginalBytes: scan.items[task.index].originalSize,
		}
		if ctx.Err() != nil {
			entry.Status = "failed"
			entry.Error = "操作已取消"
			files[task.index] = entry
			return
		}
		if outcome.err != nil {
			entry.Status = "failed"
			entry.Error = outcome.err.Error()
			files[task.index] = entry
			return
		}
		res := outcome.result
		entry.OutputFormat = res.Format
		entry.Width = res.Width
		entry.Height = res.Height
		entry.OriginalBytes = res.OriginalSize
		entry.OutputBytes = res.OutputSize
		entry.Unchanged = res.Unchanged

		if !dryRun {
			dest := destinations[task.index]
			if dest != "" {
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					entry.Status = "failed"
					entry.Error = err.Error()
					files[task.index] = entry
					return
				}
				if err := res.Save(dest); err != nil {
					entry.Status = "failed"
					entry.Error = err.Error()
					files[task.index] = entry
					return
				}
				entry.Output = dest
			} else if inPlace {
				if res.Unchanged || res.OutputSize >= res.OriginalSize {
					entry.OutputBytes = res.OriginalSize
					entry.Unchanged = true
					entry.Output = task.path
				} else {
					var err error
					if useTrash {
						err = res.OverwriteWithBackup(cliTrashItem)
					} else {
						err = res.Overwrite()
					}
					if err != nil {
						entry.Status = "failed"
						entry.Error = err.Error()
						files[task.index] = entry
						return
					}
					entry.Overwritten = true
					entry.BackedUp = useTrash
					entry.Output = task.path
				}
			}
		}

		entry.SavedBytes = entry.OriginalBytes - entry.OutputBytes
		if entry.OriginalBytes > 0 {
			entry.SavedPercent = float64(entry.SavedBytes) / float64(entry.OriginalBytes) * 100
		}
		if entry.Unchanged {
			entry.Status = "unchanged"
		} else {
			entry.Status = "ok"
		}
		files[task.index] = entry
	}

	if len(tasks) > 0 {
		runBatchPipeline(ctx, tasks, workers, process, consume)
	}
	for i, item := range scan.items {
		if files[i].Status == "" {
			files[i] = cliFileReport{
				Input:         item.path,
				SourceFormat:  item.format,
				OriginalBytes: item.originalSize,
				Status:        "failed",
				Error:         "操作已取消",
			}
		}
	}

	report := cliCompressReport{
		DryRun:    dryRun,
		InPlace:   inPlace,
		Preset:    presetName,
		Total:     len(files),
		Truncated: scan.truncated,
		Files:     files,
	}
	for _, f := range files {
		switch f.Status {
		case "ok":
			report.Succeeded++
			report.OriginalBytes += f.OriginalBytes
			report.OutputBytes += f.OutputBytes
		case "unchanged":
			report.Succeeded++
			report.Unchanged++
			report.OriginalBytes += f.OriginalBytes
			report.OutputBytes += f.OutputBytes
		default:
			report.Failed++
		}
	}
	report.SavedBytes = report.OriginalBytes - report.OutputBytes
	if report.OriginalBytes > 0 {
		report.SavedPercent = float64(report.SavedBytes) / float64(report.OriginalBytes) * 100
	}

	if jsonOut {
		if code := writeCLIJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		for _, f := range files {
			if f.Status == "failed" {
				fmt.Fprintf(stderr, "FAIL  %s  %s\n", f.Input, f.Error)
				continue
			}
			if quiet {
				continue
			}
			tag := "OK       "
			if f.Status == "unchanged" {
				tag = "UNCHANGED"
			}
			arrow := ""
			if f.Output != "" {
				arrow = " -> " + f.Output
			}
			fmt.Fprintf(stdout, "%s  %s  %s -> %s (%+.1f%%)%s\n",
				tag, f.Input, formatSize(f.OriginalBytes), formatSize(f.OutputBytes), -f.SavedPercent, arrow)
		}
		if !quiet {
			prefix := "完成"
			if dryRun {
				prefix = "试算完成"
			}
			fmt.Fprintf(stdout, "\n%s：%d 成功，%d 失败（共 %s -> %s，节省 %s / %.1f%%）\n",
				prefix, report.Succeeded, report.Failed,
				formatSize(report.OriginalBytes), formatSize(report.OutputBytes),
				formatSize(max(int64(0), report.SavedBytes)), report.SavedPercent)
		}
	}

	if report.Failed > 0 || ctx.Err() != nil {
		return 1
	}
	return 0
}

func writeCLIJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "输出 JSON 失败：%v\n", err)
		return 1
	}
	return 0
}

func reorderCLIFlags(args []string, valueFlags map[string]bool) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(arg) > 1 && arg[0] == '-' {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") {
				name := strings.TrimLeft(arg, "-")
				if valueFlags[name] && i+1 < len(args) {
					i++
					flags = append(flags, args[i])
				}
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) == 0 {
		return flags
	}
	out := make([]string, 0, len(flags)+1+len(positionals))
	out = append(out, flags...)
	out = append(out, "--")
	out = append(out, positionals...)
	return out
}
