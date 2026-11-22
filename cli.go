package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"zpic/internal/compress"
	"zpic/internal/contextio"
	"zpic/internal/imageinput"
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
	case "compress", "watch", "presets", "version", "help", "-h", "--help", "-v", "--version":
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
	return runCLIWithIO(ctx, args, os.Stdin, stdout, stderr)
}

func runCLIWithIO(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
		return runCLIPresets(ctx, args[1:], stdin, stdout, stderr)
	case "compress":
		return runCLICompress(ctx, args[1:], stdin, stdout, stderr)
	case "watch":
		return runCLIWatch(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n运行 \"z-pic help\" 查看可用命令。\n", args[0])
		return 2
	}
}

const cliOutputFormats = "keep|jpeg|png|webp|avif"

func printRootCLIHelp(w io.Writer) {
	formats := strings.Join(supportedImageFormats(), " / ")
	fmt.Fprintf(w, `Z Pic %s — 本地静态图片压缩工具（%s）

用法:
  z-pic [图片或文件夹 ...]           启动桌面界面（可选直接加载文件）
  z-pic compress [选项] <路径 ...>   无界面批量压缩图片或文件夹
  z-pic watch --output-dir <目录> [选项] <输入目录>
                                    持续压缩新到达的图片
  z-pic presets [选项]               列出已保存的压缩预设
  z-pic presets export [选项] [名称…] 导出预设为 JSON 文件或标准输出
  z-pic presets import [选项] <文件>  从 JSON 文件导入预设
  z-pic version [--json]             显示版本信息
  z-pic help [命令]                  显示帮助信息

常用示例:
  z-pic compress hero.png
  z-pic compress -o ./dist --format webp --max-dim 1920 ./photos
  z-pic compress --max-size 300KB hero.jpg
  z-pic compress --preset 均衡 --in-place ./images
  z-pic compress --dry-run --metrics --json ./photos
  cat hero.png | z-pic compress --format webp - > hero.webp
  z-pic watch --output-dir ./dist --max-size '500 KiB' ./inbox

运行 "z-pic help <命令>" 查看具体选项。
`, appVersion, formats)
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
	case "watch":
		printWatchCLIHelp(stdout)
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
	fmt.Fprintln(w, `用法:
  z-pic presets [选项]                                列出当前配置文件中的压缩预设
  z-pic presets export [选项] [预设名称 ...]          导出预设为 JSON（默认输出到标准输出）
  z-pic presets import [选项] <预设 JSON 文件 | ->    从预设 JSON 文件或标准输入导入预设

通用选项:
  --settings <path>   指定设置 JSON 文件路径（默认使用应用设置目录）

列表选项:
  --json              以 JSON 格式输出

导出选项:
  -o, --output <path> 导出到指定 JSON 文件（不指定则输出到标准输出）

导入选项:
  --overwrite         导入同名预设时覆盖已有预设（默认自动重命名保留已有预设）
  --json              以 JSON 格式输出导入摘要`)
}

type cliPNGOptions struct {
	MinQuality int `json:"min_quality"`
	MaxQuality int `json:"max_quality"`
	Colors     int `json:"colors"`
	Speed      int `json:"speed"`
}

type cliPresetEntry struct {
	Name                     string        `json:"name"`
	Active                   bool          `json:"active"`
	Quality                  int           `json:"quality"`
	Lossless                 bool          `json:"lossless"`
	Format                   string        `json:"format"`
	JPEGBackground           string        `json:"jpeg_background,omitempty"`
	MaxDimension             int           `json:"max_dimension"`
	MaxWidth                 int           `json:"max_width,omitempty"`
	MaxHeight                int           `json:"max_height,omitempty"`
	ScalePercent             int           `json:"scale_percent,omitempty"`
	TargetSizeBytes          int64         `json:"target_size_bytes"`
	TargetResizeMinDimension int           `json:"target_resize_min_dimension,omitempty"`
	Metadata                 string        `json:"metadata"`
	PNG                      cliPNGOptions `json:"png"`
	SaveMode                 string        `json:"save_mode"`
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
	switch format {
	case compress.OutputFormatJPEG, compress.OutputFormatPNG, compress.OutputFormatWebP, compress.OutputFormatAVIF:
		return string(format)
	default:
		return "keep"
	}
}

func runCLIPresets(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "export":
			return runCLIPresetsExport(args[1:], stdout, stderr)
		case "import":
			return runCLIPresetsImport(ctx, args[1:], stdin, stdout, stderr)
		}
	}
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
			Name:                     preset.name,
			Active:                   preset.name == cfg.selected,
			Quality:                  int(preset.quality),
			Lossless:                 preset.lossless,
			Format:                   cliFormatName(preset.outputFormat),
			JPEGBackground:           compress.NormalizeJPEGBackground(preset.jpegBackground),
			MaxDimension:             preset.maxDimension,
			MaxWidth:                 preset.maxWidth,
			MaxHeight:                preset.maxHeight,
			ScalePercent:             preset.scalePercent,
			TargetSizeBytes:          preset.targetSizeBytes,
			TargetResizeMinDimension: preset.targetResizeMinDimension,
			Metadata:                 cliMetadataName(preset.metadata),
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
		if entry.Format != "keep" {
			extras = append(extras, presetOutputFormatLabel(compress.OutputFormat(entry.Format)))
			if entry.Format == "jpeg" && entry.JPEGBackground != "" {
				extras = append(extras, "底色 "+strings.ToUpper(entry.JPEGBackground))
			}
		}
		switch {
		case entry.MaxDimension > 0:
			extras = append(extras, fmt.Sprintf("长边 ≤ %d px", entry.MaxDimension))
		case entry.MaxWidth > 0:
			extras = append(extras, fmt.Sprintf("宽度 ≤ %d px", entry.MaxWidth))
		case entry.MaxHeight > 0:
			extras = append(extras, fmt.Sprintf("高度 ≤ %d px", entry.MaxHeight))
		case entry.ScalePercent > 0:
			extras = append(extras, fmt.Sprintf("缩放 %d%%", entry.ScalePercent))
		}
		if entry.TargetSizeBytes > 0 {
			extras = append(extras, "体积 ≤ "+compress.FormatTargetSize(entry.TargetSizeBytes))
		}
		if entry.TargetSizeBytes > 0 && !entry.Lossless && entry.TargetResizeMinDimension > 0 {
			extras = append(extras, fmt.Sprintf("超标时缩小（长边下限 %d px）", entry.TargetResizeMinDimension))
		}
		if entry.Metadata != "keep-all" {
			extras = append(extras, "元数据:"+entry.Metadata)
		}
		fmt.Fprintf(stdout, "%s%s  (%s)\n", marker, entry.Name, strings.Join(extras, " · "))
	}
	return 0
}

func runCLIPresetsExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("presets export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	settingsPath := fs.String("settings", "", "settings file path")
	var outputPath string
	fs.StringVar(&outputPath, "output", "", "output file path")
	fs.StringVar(&outputPath, "o", "", "output file path")
	if _, err := parseCLIFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPresetsCLIHelp(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	cfg, _, err := loadCLISettings(*settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "读取预设失败：%v\n", err)
		return 1
	}
	selected := cfg.presets
	if names := fs.Args(); len(names) > 0 {
		byName := make(map[string]compressionPreset, len(cfg.presets))
		for _, preset := range cfg.presets {
			byName[preset.name] = preset
		}
		seen := make(map[string]bool, len(names))
		selected = make([]compressionPreset, 0, len(names))
		for _, name := range names {
			preset, ok := byName[name]
			if !ok {
				fmt.Fprintf(stderr, "找不到预设 %q；运行 \"z-pic presets\" 查看可用预设。\n", name)
				return 2
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			selected = append(selected, preset)
		}
	}
	if outputPath == "" {
		data, err := marshalPresetBundle(selected)
		if err != nil {
			fmt.Fprintf(stderr, "导出预设失败：%v\n", err)
			return 1
		}
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "输出预设失败：%v\n", err)
			return 1
		}
		return 0
	}
	if err := writePresetBundleFile(outputPath, selected); err != nil {
		fmt.Fprintf(stderr, "导出预设失败：%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "已导出 %d 个预设到 %s\n", len(selected), outputPath)
	return 0
}

type cliPresetImportReport struct {
	SettingsPath string `json:"settings_path"`
	Added        int    `json:"added"`
	Updated      int    `json:"updated"`
	Total        int    `json:"total"`
}

func runCLIPresetsImport(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("presets import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	settingsPath := fs.String("settings", "", "settings file path")
	overwrite := fs.Bool("overwrite", false, "overwrite existing presets with the same name")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseCLIFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPresetsCLIHelp(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "参数错误：%v\n", err)
		return 2
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" {
		fmt.Fprintln(stderr, "请提供一个要导入的预设 JSON 文件路径（或使用 - 从标准输入读取）。\n运行 \"z-pic help presets\" 查看用法。")
		return 2
	}
	var (
		imported []compressionPreset
		err      error
	)
	if fs.Arg(0) == "-" {
		if stdin == nil {
			fmt.Fprintln(stderr, "读取预设失败：无法读取标准输入")
			return 1
		}
		raw, readErr := io.ReadAll(io.LimitReader(contextio.NewReader(ctx, stdin), maxSettingsBytes+1))
		if readErr != nil {
			fmt.Fprintf(stderr, "读取标准输入失败：%v\n", readErr)
			return 1
		}
		if len(raw) == 0 {
			fmt.Fprintln(stderr, "读取预设失败：标准输入为空")
			return 1
		}
		imported, err = unmarshalPresetBundle(raw)
	} else {
		imported, err = readPresetBundleFile(fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintf(stderr, "读取预设文件失败：%v\n", err)
		return 1
	}
	cfg, path, err := loadCLISettings(*settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "读取现有设置失败：%v\n", err)
		return 1
	}
	if path == "" {
		fmt.Fprintln(stderr, "无法确定设置文件保存路径，请通过 --settings 指定。")
		return 1
	}
	merged, added, updated, _ := mergeImportedPresets(cfg.presets, imported, *overwrite)
	cfg.presets = merged
	if err := saveFullSettings(path, cfg); err != nil {
		fmt.Fprintf(stderr, "保存设置失败：%v\n", err)
		return 1
	}
	if *jsonOut {
		return writeCLIJSON(stdout, stderr, cliPresetImportReport{
			SettingsPath: path,
			Added:        added,
			Updated:      updated,
			Total:        len(merged),
		})
	}
	fmt.Fprintf(stdout, "已导入预设（新增 %d，覆盖 %d，共 %d 个预设）→ %s\n", added, updated, len(merged), path)
	return 0
}

func printCompressCLIHelp(w io.Writer) {
	formats := strings.Join(supportedImageFormats(), " / ")
	fmt.Fprintf(w, `用法: z-pic compress [选项] <图片或文件夹 ...>

无界面批量压缩静态 %s 图片（默认递归扫描目录）。
默认不会覆盖原文件，会在原目录生成带 "-zpic" 后缀的防冲突副本。

选项:
  -p, --preset <name>      使用已保存预设作为基础参数
      --active-preset      使用桌面端当前选中的预设作为基础参数
      --settings <path>    指定设置 JSON 文件路径
  -q, --quality <1-100>    %s 质量并联动调整 PNG 量化参数
      --lossless           无损模式（转为 JPEG 时使用有损编码）
      --format <%s> 输出格式（jpeg 可用 jpg，keep 时 HEIC 转为 JPEG 或 PNG）
                           AVIF 编解码仅在 Apple 芯片的 macOS 上可用
      --jpeg-bg <color>    透明图转为 JPEG 时的填充底色（white | black | #RRGGBB）
      --max-dim <px>       限制长边像素上限（0 表示原尺寸，范围 %d–%d）
      --max-width <px>     限制显示宽度，像素范围与 --max-dim 相同
      --max-height <px>    限制显示高度，像素范围与 --max-dim 相同
      --scale-percent <N>  按原尺寸的 N%% 等比缩小（1–100）
                           四种尺寸选项择一，覆盖预设尺寸；0 恢复原尺寸
      --max-size <size>    尝试将每张图片压到目标体积以内，如 300KB 或 1MiB（0 表示关闭）
      --target-resize-min <px>  超标时自动缩小，指定长边下限（0 表示关闭，范围 %d–%d；无损模式下停用）
      --metadata <policy>  元数据策略：keep-all | keep-icc | strip
  -o, --output <path>      输出文件路径（单图）、输出目录（批量/多图）或 -（单图写入标准输出）
      --in-place           仅在体积减小时覆盖同格式原图（格式转换时在原目录另存副本）
      --trash              覆盖前将原图移入系统废纸篓（仅与 --in-place 一起使用）
      --suffix <text>      副本文件名后缀（默认 -zpic）
      --no-recursive       只扫描指定目录的顶层
      --dry-run            仅在内存中试算压缩结果，不写入磁盘
      --metrics            计算并输出客观画质指标（SSIM / PSNR / 像素变化占比）
  -j, --jobs <N>           并发任务数（0 表示自动）
      --json               以 JSON 格式输出完整报告
      --quiet              静默模式（仅输出错误）

管道用法:
  输入路径填 - 表示从标准输入读取单张图片；未指定 -o/--dry-run/--json 时默认将压缩结果写入标准输出。
  例: cat hero.png | z-pic compress --format webp -q 80 - > hero.webp
`, formats, lossyQualityFormats(), cliOutputFormats, compress.MinResizeDimension, compress.MaxResizeDimension,
		compress.MinResizeDimension, compress.MaxResizeDimension)
}

type cliQualityMetrics struct {
	PSNRDB         *float64 `json:"psnr_db,omitempty"`
	SSIM           float64  `json:"ssim"`
	ChangedPercent float64  `json:"changed_percent"`
	MaxChannelDiff uint8    `json:"max_channel_diff"`
	Identical      bool     `json:"identical"`
	Resized        bool     `json:"resized,omitempty"`
}

func makeCLIQualityMetrics(m compress.QualityMetrics) *cliQualityMetrics {
	out := &cliQualityMetrics{
		SSIM:           math.Round(m.SSIM*10000) / 10000,
		ChangedPercent: math.Round(m.ChangedPercent*100) / 100,
		MaxChannelDiff: m.MaxChannelDiff,
		Identical:      m.Identical,
		Resized:        m.Resized,
	}
	if !m.Identical && !math.IsInf(m.PSNR, 0) && !math.IsNaN(m.PSNR) {
		v := math.Round(m.PSNR*100) / 100
		out.PSNRDB = &v
	}
	return out
}

type cliFileReport struct {
	Input            string             `json:"input"`
	Output           string             `json:"output,omitempty"`
	Status           string             `json:"status"`
	SourceFormat     string             `json:"source_format,omitempty"`
	OutputFormat     string             `json:"output_format,omitempty"`
	Width            int                `json:"width,omitempty"`
	Height           int                `json:"height,omitempty"`
	OriginalBytes    int64              `json:"original_bytes"`
	OutputBytes      int64              `json:"output_bytes"`
	SavedBytes       int64              `json:"saved_bytes"`
	SavedPercent     float64            `json:"saved_percent"`
	AppliedQuality   int                `json:"applied_quality,omitempty"`
	TargetSizeMissed bool               `json:"target_size_missed,omitempty"`
	TargetResized    bool               `json:"target_resized,omitempty"`
	OriginalWidth    int                `json:"original_width,omitempty"`
	OriginalHeight   int                `json:"original_height,omitempty"`
	Unchanged        bool               `json:"unchanged,omitempty"`
	Overwritten      bool               `json:"overwritten,omitempty"`
	BackedUp         bool               `json:"backed_up,omitempty"`
	Metrics          *cliQualityMetrics `json:"metrics,omitempty"`
	MetricsError     string             `json:"metrics_error,omitempty"`
	Error            string             `json:"error,omitempty"`
}

type cliCompressReport struct {
	DryRun           bool            `json:"dry_run"`
	InPlace          bool            `json:"in_place"`
	Preset           string          `json:"preset,omitempty"`
	Total            int             `json:"total"`
	Succeeded        int             `json:"succeeded"`
	Failed           int             `json:"failed"`
	Unchanged        int             `json:"unchanged"`
	TargetSizeBytes  int64           `json:"target_size_bytes,omitempty"`
	TargetSizeMissed int             `json:"target_size_missed,omitempty"`
	Truncated        bool            `json:"truncated,omitempty"`
	OriginalBytes    int64           `json:"original_bytes"`
	OutputBytes      int64           `json:"output_bytes"`
	SavedBytes       int64           `json:"saved_bytes"`
	SavedPercent     float64         `json:"saved_percent"`
	Files            []cliFileReport `json:"files"`
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
	case "jpeg", "jpg":
		return compress.OutputFormatJPEG, nil
	case "png":
		return compress.OutputFormatPNG, nil
	case "webp":
		return compress.OutputFormatWebP, nil
	case "avif":
		return compress.OutputFormatAVIF, nil
	default:
		return "", fmt.Errorf("无效的输出格式 %q（支持 %s）", raw, cliOutputFormats)
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
	case "avif":
		return ext == ".avif"
	default:
		return false
	}
}

type cliCompressionFlags struct {
	presetName      string
	activePreset    bool
	settingsPath    string
	quality         int
	lossless        bool
	format          string
	jpegBg          string
	maxDim          int
	maxWidth        int
	maxHeight       int
	scalePercent    int
	maxSize         string
	targetResizeMin int
	metadata        string
}

func (f *cliCompressionFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.presetName, "preset", "", "preset name")
	fs.StringVar(&f.presetName, "p", "", "preset name")
	fs.BoolVar(&f.activePreset, "active-preset", false, "use the currently active preset from settings")
	fs.StringVar(&f.settingsPath, "settings", "", "settings file path")
	fs.IntVar(&f.quality, "quality", 80, "quality 1-100")
	fs.IntVar(&f.quality, "q", 80, "quality 1-100")
	fs.BoolVar(&f.lossless, "lossless", false, "lossless compression")
	fs.StringVar(&f.format, "format", "", "output format: "+cliOutputFormats+" (jpg is an alias for jpeg)")
	fs.StringVar(&f.jpegBg, "jpeg-bg", "", "background color for transparent images converted to JPEG: white|black|#RRGGBB")
	fs.IntVar(&f.maxDim, "max-dim", 0, "max long-edge dimension in pixels")
	fs.IntVar(&f.maxWidth, "max-width", 0, "max displayed width in pixels")
	fs.IntVar(&f.maxHeight, "max-height", 0, "max displayed height in pixels")
	fs.IntVar(&f.scalePercent, "scale-percent", 0, "resize to a percentage of the original dimensions")
	fs.StringVar(&f.maxSize, "max-size", "", "target output size")
	fs.IntVar(&f.targetResizeMin, "target-resize-min", 0, "minimum long edge for automatic target-size resizing")
	fs.StringVar(&f.metadata, "metadata", "", "metadata policy: keep-all|keep-icc|strip")
}

func (f cliCompressionFlags) loadSettings(setFlags map[string]bool) (appSettings, error) {
	if f.presetName == "" && !f.activePreset && !setFlags["settings"] {
		return appSettings{}, nil
	}
	cfg, _, err := loadCLISettings(f.settingsPath)
	if err != nil {
		return appSettings{}, fmt.Errorf("读取设置失败：%w", err)
	}
	return cfg, nil
}

func (f cliCompressionFlags) options(cfg appSettings, setFlags map[string]bool) (compress.Options, *compressionPreset, error) {
	opts := compress.Options{Quality: 80, PNG: compress.DefaultPNGOptions()}
	if f.presetName != "" && f.activePreset {
		return opts, nil, errors.New("参数冲突：--preset 与 --active-preset 不能同时使用。")
	}
	var basePreset *compressionPreset
	if f.presetName != "" {
		for i := range cfg.presets {
			if cfg.presets[i].name == f.presetName {
				basePreset = &cfg.presets[i]
				break
			}
		}
		if basePreset == nil {
			return opts, nil, fmt.Errorf("找不到预设 %q；运行 \"z-pic presets\" 查看可用预设。", f.presetName)
		}
		opts = basePreset.compressionOptions()
	} else if f.activePreset {
		for i := range cfg.presets {
			if cfg.presets[i].name == cfg.selected {
				basePreset = &cfg.presets[i]
				break
			}
		}
		if basePreset == nil && len(cfg.presets) > 0 {
			basePreset = &cfg.presets[0]
		}
		if basePreset != nil {
			opts = basePreset.compressionOptions()
		}
	}

	if setFlags["quality"] || setFlags["q"] {
		if f.quality < 1 || f.quality > 100 {
			return opts, nil, errors.New("参数错误：--quality 必须在 1 到 100 之间。")
		}
		opts.Quality = f.quality
		opts.PNG = cliPNGOptionsForQuality(f.quality)
	}
	if setFlags["lossless"] {
		opts.Lossless = f.lossless
	}
	if setFlags["format"] {
		parsed, err := parseCLIOutputFormat(f.format)
		if err != nil {
			return opts, nil, fmt.Errorf("参数错误：%w", err)
		}
		opts.OutputFormat = parsed
	}
	if setFlags["jpeg-bg"] {
		if err := compress.ValidateJPEGBackground(f.jpegBg); err != nil {
			return opts, nil, fmt.Errorf("参数错误：--jpeg-bg：%w", err)
		}
		opts.JPEGBackground = compress.NormalizeJPEGBackground(f.jpegBg)
	}
	var resizeFlag string
	for _, name := range [...]string{"max-dim", "max-width", "max-height", "scale-percent"} {
		if !setFlags[name] {
			continue
		}
		if resizeFlag != "" {
			return opts, nil, fmt.Errorf("参数冲突：--%s 与 --%s 不能同时使用。", resizeFlag, name)
		}
		resizeFlag = name
	}
	if resizeFlag != "" {
		if err := compress.ValidateResizeOptions(f.maxDim, f.maxWidth, f.maxHeight, f.scalePercent); err != nil {
			return opts, nil, fmt.Errorf("参数错误：--%s：%w", resizeFlag, err)
		}
		opts.MaxDimension, opts.MaxWidth, opts.MaxHeight, opts.ScalePercent = f.maxDim, f.maxWidth, f.maxHeight, f.scalePercent
	}
	if setFlags["max-size"] {
		parsed, err := compress.ParseTargetSize(f.maxSize)
		if err != nil {
			return opts, nil, fmt.Errorf("参数错误：%w", err)
		}
		opts.TargetSizeBytes = parsed
	}
	if setFlags["target-resize-min"] {
		if err := compress.ValidateMaxDimension(f.targetResizeMin); err != nil {
			return opts, nil, fmt.Errorf("参数错误：--target-resize-min 必须为 0（关闭）或 %d 到 %d 之间的像素数。", compress.MinResizeDimension, compress.MaxResizeDimension)
		}
		opts.TargetResizeMinDimension = f.targetResizeMin
	}
	if setFlags["metadata"] {
		parsed, err := parseCLIMetadataPolicy(f.metadata)
		if err != nil {
			return opts, nil, fmt.Errorf("参数错误：%w", err)
		}
		opts.Metadata = parsed
	}
	return opts, basePreset, nil
}

func runCLICompress(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compress", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var compressionFlags cliCompressionFlags
	compressionFlags.bind(fs)
	var (
		outputPath  string
		inPlace     bool
		useTrash    bool
		suffix      string
		noRecursive bool
		dryRun      bool
		metricsOut  bool
		jobs        int
		jsonOut     bool
		quiet       bool
	)
	fs.StringVar(&outputPath, "output", "", "output file, directory, or - for stdout")
	fs.StringVar(&outputPath, "o", "", "output file, directory, or - for stdout")
	fs.BoolVar(&inPlace, "in-place", false, "overwrite source files in place when smaller")
	fs.BoolVar(&useTrash, "trash", false, "move source files to the Trash before overwriting")
	fs.StringVar(&suffix, "suffix", "", "copy filename suffix")
	fs.BoolVar(&noRecursive, "no-recursive", false, "scan only the top level of input directories")
	fs.BoolVar(&dryRun, "dry-run", false, "estimate compression without writing files")
	fs.BoolVar(&metricsOut, "metrics", false, "compute PSNR/SSIM quality metrics")
	fs.IntVar(&jobs, "jobs", 0, "worker concurrency")
	fs.IntVar(&jobs, "j", 0, "worker concurrency")
	fs.BoolVar(&jsonOut, "json", false, "JSON report")
	fs.BoolVar(&quiet, "quiet", false, "suppress normal output")

	setFlags, err := parseCLIFlags(fs, args)
	if err != nil {
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
		fmt.Fprintln(stderr, "请提供至少一个图片文件或文件夹（或使用 - 从标准输入读取）。\n运行 \"z-pic help compress\" 查看用法。")
		return 2
	}

	stdinInput := slices.Contains(inputs, "-")
	stdoutOutput := outputPath == "-"
	if stdinInput && !setFlags["output"] && !setFlags["o"] && !dryRun && !jsonOut {
		stdoutOutput = true
	}
	if stdinInput && len(inputs) > 1 {
		fmt.Fprintln(stderr, "参数冲突：标准输入（-）一次只能处理单张图片，不能与其他路径混用。")
		return 2
	}
	if stdinInput && (inPlace || useTrash) {
		fmt.Fprintln(stderr, "参数冲突：标准输入（-）不支持 --in-place 或 --trash。")
		return 2
	}
	if stdinInput && setFlags["suffix"] {
		fmt.Fprintln(stderr, "参数冲突：标准输入（-）不支持 --suffix，请使用 -o 指定输出文件或标准输出（-）。")
		return 2
	}
	if stdoutOutput && (inPlace || dryRun || jsonOut) {
		fmt.Fprintln(stderr, "参数冲突：输出到标准输出（-o -）不能与 --in-place、--dry-run 或 --json 同时使用。")
		return 2
	}
	if stdoutOutput && len(inputs) > 1 {
		fmt.Fprintln(stderr, "参数冲突：输出到标准输出（-o -）仅支持单张图片输入。")
		return 2
	}
	if stdinInput && !stdoutOutput && !dryRun && outputPath == "" {
		if setFlags["output"] || setFlags["o"] {
			fmt.Fprintln(stderr, "参数错误：--output / -o 不能为空，请指定输出文件路径或使用 - 输出到标准输出。")
		} else {
			fmt.Fprintln(stderr, "参数错误：从标准输入读取图片并输出 --json 时，请搭配 --dry-run 或 -o <输出文件路径>。")
		}
		return 2
	}

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

	cfg, err := compressionFlags.loadSettings(setFlags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if inPlace && !setFlags["trash"] {
		useTrash = cfg.backupToTrash
	}
	opts, basePreset, err := compressionFlags.options(cfg, setFlags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	resolvedPresetName := compressionFlags.presetName
	if basePreset != nil {
		resolvedPresetName = basePreset.name
	}

	if stdinInput {
		return runCLICompressStdin(ctx, opts, resolvedPresetName, outputPath, stdoutOutput, dryRun, metricsOut, jsonOut, quiet, stdin, stdout, stderr)
	}

	scan, err := scanBatchPathsWithOptions(ctx, inputs, nil, nil, 0, !noRecursive)
	if err != nil {
		fmt.Fprintf(stderr, "扫描输入失败：%v\n", err)
		return 1
	}
	if len(scan.items) == 0 {
		fmt.Fprintln(stderr, noSupportedImagesMessage())
		return 2
	}
	if stdoutOutput {
		if len(scan.items) != 1 || scan.items[0].sourceDir != "" {
			fmt.Fprintln(stderr, "参数冲突：输出到标准输出（-o -）仅支持单个图片文件输入。")
			return 2
		}
		item := scan.items[0]
		if item.invalidInput {
			fmt.Fprintf(stderr, "FAIL  %s  %s\n", item.path, item.errMsg)
			return 1
		}
		opts.SkipOverwriteSnapshot = true
		res, err := compress.File(ctx, item.path, opts)
		if err != nil {
			fmt.Fprintf(stderr, "FAIL  %s  %v\n", item.path, err)
			return 1
		}
		if _, err := stdout.Write(res.Data); err != nil {
			fmt.Fprintf(stderr, "写入标准输出失败：%v\n", err)
			return 1
		}
		if metricsOut && !quiet {
			origBytes, readErr := imageinput.Read(ctx, item.path)
			if readErr != nil {
				fmt.Fprintf(stderr, "WARN  %s  无法计算画质指标：%v\n", item.path, readErr)
			} else {
				printCLIPipeMetrics(ctx, stderr, origBytes, res)
			}
		}
		return 0
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
			isImagePath := isSupportedImageExt(outputPath) || expectedExtMatchesFormat(filepath.Ext(outputPath), "avif")
			if singleInputIsFile && !isExistingDir && isImagePath {
				item := scan.items[0]
				if !item.invalidInput {
					effectiveFmt := compress.ResolveOutputFormat(item.effectiveSourceFormat(), opts.OutputFormat, opts.Lossless)
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
			sourceFormat: item.effectiveSourceFormat(),
			outputFormat: opts.OutputFormat,
			lossless:     opts.Lossless,
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
		} else {
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
	metricsByIndex := make([]*cliQualityMetrics, len(scan.items))
	metricsErrByIndex := make([]string, len(scan.items))
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
		if err == nil && result != nil && metricsOut {
			if result.Unchanged {
				metricsByIndex[task.index] = makeCLIQualityMetrics(compress.QualityMetrics{
					PSNR:      math.Inf(1),
					SSIM:      1,
					Identical: true,
				})
			} else if origBytes, readErr := imageinput.Read(ctx, task.path); readErr != nil {
				metricsErrByIndex[task.index] = readErr.Error()
			} else if m, mErr := compress.MeasureQualityMetricsBytes(ctx, origBytes, result.Data); mErr != nil {
				metricsErrByIndex[task.index] = mErr.Error()
			} else {
				metricsByIndex[task.index] = makeCLIQualityMetrics(m)
			}
		}
		return batchCompressionOutcome{result: result, err: err, started: started, release: release}
	}

	report := cliCompressReport{
		DryRun:          dryRun,
		InPlace:         inPlace,
		Preset:          resolvedPresetName,
		Total:           len(files),
		TargetSizeBytes: opts.TargetSizeBytes,
		Truncated:       scan.truncated,
		Files:           files,
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
					entry.Width, entry.Height = scan.items[task.index].width, scan.items[task.index].height
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
			entry.TargetSizeMissed = res.TargetSizeBytes > 0 && entry.OutputBytes > res.TargetSizeBytes
		} else {
			entry.Status = "ok"
			entry.AppliedQuality = res.AppliedQuality
			entry.TargetResized = res.TargetResized
			if entry.TargetResized {
				entry.OriginalWidth, entry.OriginalHeight = scan.items[task.index].width, scan.items[task.index].height
			}
			entry.TargetSizeMissed = res.TargetSizeMissed
		}
		if metricsOut {
			if entry.Unchanged {
				entry.Metrics = makeCLIQualityMetrics(compress.QualityMetrics{
					PSNR:      math.Inf(1),
					SSIM:      1,
					Identical: true,
				})
			} else {
				entry.Metrics = metricsByIndex[task.index]
				entry.MetricsError = metricsErrByIndex[task.index]
			}
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

	for _, f := range files {
		switch f.Status {
		case "ok", "unchanged":
			report.Succeeded++
			if f.Unchanged {
				report.Unchanged++
			}
			if f.TargetSizeMissed {
				report.TargetSizeMissed++
			}
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
			printCLIFileResult(stdout, f)
			if f.MetricsError != "" {
				fmt.Fprintf(stderr, "WARN  %s  无法计算画质指标：%s\n", f.Input, f.MetricsError)
			}
		}
		if !quiet {
			prefix := "完成"
			if dryRun {
				prefix = "试算完成"
			}
			targetNote := ""
			if report.TargetSizeBytes > 0 {
				targetNote = "，目标体积 ≤ " + compress.FormatTargetSize(report.TargetSizeBytes)
			}
			if report.TargetSizeMissed > 0 {
				targetNote += fmt.Sprintf("，%d 未达目标", report.TargetSizeMissed)
			}
			fmt.Fprintf(stdout, "\n%s：%d 成功，%d 失败%s（共 %s -> %s，节省 %s / %.1f%%）\n",
				prefix, report.Succeeded, report.Failed, targetNote,
				formatSize(report.OriginalBytes), formatSize(report.OutputBytes),
				formatSize(max(int64(0), report.SavedBytes)), report.SavedPercent)
		}
	}

	if report.Failed > 0 || ctx.Err() != nil {
		return 1
	}
	return 0
}

func runCLICompressStdin(
	ctx context.Context,
	opts compress.Options,
	presetName string,
	outputPath string,
	stdoutOutput bool,
	dryRun bool,
	metricsOut bool,
	jsonOut bool,
	quiet bool,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	fail := func(msg string, entry cliFileReport) int {
		entry.Input = "-"
		entry.Status = "failed"
		entry.Error = msg
		if jsonOut {
			report := cliCompressReport{
				DryRun:          dryRun,
				Preset:          presetName,
				Total:           1,
				Failed:          1,
				TargetSizeBytes: opts.TargetSizeBytes,
				Files:           []cliFileReport{entry},
			}
			if code := writeCLIJSON(stdout, stderr, report); code != 0 {
				return code
			}
			return 1
		}
		fmt.Fprintf(stderr, "FAIL  -  %s\n", msg)
		return 1
	}

	if stdin == nil {
		return fail("读取标准输入失败：输入流不可用", cliFileReport{})
	}
	raw, err := io.ReadAll(io.LimitReader(contextio.NewReader(ctx, stdin), int64(imageinput.MaxSize)+1))
	if err != nil {
		return fail(fmt.Sprintf("读取标准输入失败：%v", err), cliFileReport{})
	}
	if len(raw) == 0 {
		return fail("读取标准输入失败：输入为空", cliFileReport{})
	}
	if len(raw) > imageinput.MaxSize {
		return fail("输入图片超过 64 MiB 限制", cliFileReport{OriginalBytes: int64(len(raw))})
	}
	info, err := compress.InspectBytes(ctx, raw)
	if err != nil {
		return fail(fmt.Sprintf("无法识别标准输入图片：%v", err), cliFileReport{OriginalBytes: int64(len(raw))})
	}

	if !dryRun && !stdoutOutput && outputPath != "" {
		if stat, statErr := os.Stat(outputPath); statErr == nil && stat.IsDir() {
			fmt.Fprintf(stderr, "参数错误：从标准输入读取图片时，-o 需指定具体输出文件路径而非目录：%s\n", outputPath)
			return 2
		}
		effectiveFmt := compress.ResolveOutputFormat(info.Format, opts.OutputFormat, opts.Lossless)
		if !expectedExtMatchesFormat(filepath.Ext(outputPath), effectiveFmt) {
			fmt.Fprintf(stderr, "参数错误：输出文件扩展名 %q 与目标格式 %s 不匹配。\n", filepath.Ext(outputPath), strings.ToUpper(effectiveFmt))
			return 2
		}
	}

	res, err := compress.BytesWithoutSource(ctx, raw, opts)
	if err != nil {
		return fail(err.Error(), cliFileReport{
			SourceFormat:  info.Format,
			OriginalBytes: int64(len(raw)),
		})
	}

	if stdoutOutput {
		if _, err := stdout.Write(res.Data); err != nil {
			fmt.Fprintf(stderr, "写入标准输出失败：%v\n", err)
			return 1
		}
		if metricsOut && !quiet {
			printCLIPipeMetrics(ctx, stderr, raw, res)
		}
		return 0
	}

	entry := cliFileReport{
		Input:         "-",
		SourceFormat:  info.Format,
		OutputFormat:  res.Format,
		Width:         res.Width,
		Height:        res.Height,
		OriginalBytes: res.OriginalSize,
		OutputBytes:   res.OutputSize,
		SavedBytes:    res.OriginalSize - res.OutputSize,
		Unchanged:     res.Unchanged,
	}
	if entry.OriginalBytes > 0 {
		entry.SavedPercent = float64(entry.SavedBytes) / float64(entry.OriginalBytes) * 100
	}
	if !dryRun && outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return fail(err.Error(), entry)
		}
		if err := res.Save(outputPath); err != nil {
			return fail(err.Error(), entry)
		}
		entry.Output = outputPath
	}
	if entry.Unchanged {
		entry.Status = "unchanged"
		entry.TargetSizeMissed = res.TargetSizeBytes > 0 && entry.OutputBytes > res.TargetSizeBytes
	} else {
		entry.Status = "ok"
		entry.AppliedQuality = res.AppliedQuality
		entry.TargetResized = res.TargetResized
		if entry.TargetResized {
			entry.OriginalWidth, entry.OriginalHeight = info.Width, info.Height
		}
		entry.TargetSizeMissed = res.TargetSizeMissed
	}
	if metricsOut {
		if entry.Unchanged {
			entry.Metrics = makeCLIQualityMetrics(compress.QualityMetrics{
				PSNR:      math.Inf(1),
				SSIM:      1,
				Identical: true,
			})
		} else if m, mErr := compress.MeasureQualityMetricsBytes(ctx, raw, res.Data); mErr != nil {
			entry.MetricsError = mErr.Error()
		} else {
			entry.Metrics = makeCLIQualityMetrics(m)
		}
	}

	report := cliCompressReport{
		DryRun:          dryRun,
		Preset:          presetName,
		Total:           1,
		Succeeded:       1,
		TargetSizeBytes: opts.TargetSizeBytes,
		OriginalBytes:   entry.OriginalBytes,
		OutputBytes:     entry.OutputBytes,
		SavedBytes:      entry.SavedBytes,
		SavedPercent:    entry.SavedPercent,
		Files:           []cliFileReport{entry},
	}
	if entry.Unchanged {
		report.Unchanged = 1
	}
	if entry.TargetSizeMissed {
		report.TargetSizeMissed = 1
	}
	if jsonOut {
		return writeCLIJSON(stdout, stderr, report)
	}
	if !quiet {
		printCLIFileResult(stdout, entry)
		if entry.MetricsError != "" {
			fmt.Fprintf(stderr, "WARN  -  无法计算画质指标：%s\n", entry.MetricsError)
		}
	}
	return 0
}

func printCLIPipeMetrics(ctx context.Context, w io.Writer, origBytes []byte, res *compress.Result) {
	if res == nil {
		return
	}
	var m compress.QualityMetrics
	if res.Unchanged {
		m = compress.QualityMetrics{PSNR: math.Inf(1), SSIM: 1, Identical: true}
	} else {
		measured, err := compress.MeasureQualityMetricsBytes(ctx, origBytes, res.Data)
		if err != nil {
			fmt.Fprintf(w, "WARN  无法计算画质指标：%v\n", err)
			return
		}
		m = measured
	}
	fmt.Fprintf(w, "METRICS  %s -> %s · %s\n",
		formatSize(res.OriginalSize), formatSize(res.OutputSize), formatQualityMetricsSummary(m))
}

func printCLIFileResult(w io.Writer, f cliFileReport) {
	tag := "OK       "
	if f.Status == "unchanged" {
		tag = "UNCHANGED"
	}
	arrow := ""
	if f.Output != "" {
		arrow = " -> " + f.Output
	}
	notes := ""
	if f.TargetResized {
		notes = fmt.Sprintf(" [自动缩小 %d × %d → %d × %d]", f.OriginalWidth, f.OriginalHeight, f.Width, f.Height)
	}
	if f.TargetSizeMissed {
		notes += " [未达目标]"
	}
	if f.Metrics != nil {
		if f.Metrics.Identical {
			notes += " [无损一致 · SSIM 1.000]"
		} else if f.Metrics.PSNRDB != nil {
			notes += fmt.Sprintf(" [SSIM %.3f · PSNR %.1f dB]", f.Metrics.SSIM, *f.Metrics.PSNRDB)
		} else {
			notes += fmt.Sprintf(" [SSIM %.3f]", f.Metrics.SSIM)
		}
	}
	fmt.Fprintf(w, "%s  %s  %s -> %s (%+.1f%%)%s%s\n",
		tag, f.Input, formatSize(f.OriginalBytes), formatSize(f.OutputBytes), -f.SavedPercent, arrow, notes)
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

func parseCLIFlags(fs *flag.FlagSet, args []string) (map[string]bool, error) {
	valueFlags := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) {
		boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool })
		valueFlags[f.Name] = !ok || !boolFlag.IsBoolFlag()
	})
	if err := fs.Parse(reorderCLIFlags(args, valueFlags)); err != nil {
		return nil, err
	}
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	return setFlags, nil
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
