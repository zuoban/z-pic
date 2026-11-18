package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

type presetSaveMode string

const (
	presetSaveOriginal presetSaveMode = ""
	presetSaveAsk      presetSaveMode = "ask"
	presetSaveFixed    presetSaveMode = "fixed"
)

const (
	defaultSubfolderName  = "zpic"
	defaultFilenameSuffix = "-zpic"
)

type presetSaveOptions struct {
	Mode         presetSaveMode `json:"mode,omitempty"`
	Directory    string         `json:"directory,omitempty"`
	UseSubfolder bool           `json:"use_subfolder,omitempty"`
	Subfolder    string         `json:"subfolder,omitempty"`
	UseSuffix    bool           `json:"use_suffix,omitempty"`
	Suffix       string         `json:"suffix,omitempty"`
}

func validOutputComponent(value string) bool {
	return value != "." && !strings.Contains(value, "..") && !strings.ContainsAny(value, `/\\`) && !strings.ContainsFunc(value, unicode.IsControl)
}

func (o presetSaveOptions) supportsSubfolder() bool {
	return o.Mode == presetSaveOriginal || o.Mode == presetSaveFixed
}

func (o presetSaveOptions) validate() error {
	_, err := o.validateField()
	return err
}

func (o presetSaveOptions) validateField() (string, error) {
	if o.Mode != presetSaveOriginal && o.Mode != presetSaveAsk && o.Mode != presetSaveFixed {
		return "preset-save-mode", errors.New("未知的保存方式")
	}
	for _, value := range []struct{ field, text string }{
		{"preset-output-directory", o.Directory}, {"preset-subfolder-name", o.Subfolder}, {"preset-suffix-value", o.Suffix},
	} {
		if !utf8.ValidString(value.text) {
			return value.field, errors.New("保存设置包含无效的 UTF-8 字符")
		}
	}
	if strings.ContainsRune(o.Directory, 0) {
		return "preset-output-directory", errors.New("输出目录不能包含空字符")
	}
	if o.Mode == presetSaveFixed && o.Directory != "" && !filepath.IsAbs(o.Directory) {
		return "preset-output-directory", errors.New("固定输出目录必须是绝对路径")
	}
	if o.supportsSubfolder() && o.UseSubfolder && !validOutputComponent(o.Subfolder) {
		return "preset-subfolder-name", errors.New("子文件夹必须是单个合法名称")
	}
	if o.UseSuffix && !validOutputComponent(o.Suffix) {
		return "preset-suffix-value", errors.New("文件名后缀不能包含路径或控制字符")
	}
	return "", nil
}

func (o presetSaveOptions) replacesOriginal() bool {
	return o.Mode == presetSaveOriginal && !o.UseSubfolder && !o.UseSuffix
}

func (s *application) applyPresetOutput(o presetSaveOptions) {
	if s.output == o {
		return
	}
	s.output = o
	s.configureBatchPresetOutput()
}

func (s *application) configureBatchPresetOutput() {
	if s.batch == nil {
		return
	}
	mode, directory := batchOutputFolder, ""
	if s.output.replacesOriginal() {
		mode = batchOutputOverwrite
	}
	if s.output.Mode == presetSaveFixed {
		directory = s.output.Directory
	}
	s.batch.outputMode, s.batch.outputDir = mode, directory
	s.resetBatchRunState()
}

func (o presetSaveOptions) copyPath(source, root, rel string, used map[string]bool) (string, error) {
	return o.copyPathWithPlanner(source, root, rel, &batchOutputPlanner{used: used})
}

func (o presetSaveOptions) copyPathWithPlanner(source, root, rel string, planner *batchOutputPlanner) (string, error) {
	if err := o.validate(); err != nil {
		return "", err
	}
	if o.Mode == presetSaveOriginal {
		root, rel = filepath.Dir(source), filepath.Base(source)
	}
	if root == "" {
		return "", errors.New("请选择固定输出目录")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("无法读取输出文件夹：%w", err)
	}
	if !info.IsDir() {
		return "", errors.New("输出位置不是文件夹")
	}
	if o.supportsSubfolder() && o.UseSubfolder {
		name := o.Subfolder
		if name == "" {
			name = defaultSubfolderName
		}
		subfolder := filepath.Join(root, name)
		ctx := planner.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := validateOutputDirectory(ctx, root, subfolder); err != nil {
			return "", err
		}
		root = subfolder
	}
	rel = sanitizeRelPath(rel, filepath.Base(source))
	if o.UseSuffix {
		suffix := o.Suffix
		if suffix == "" {
			suffix = defaultFilenameSuffix
		}
		ext := filepath.Ext(rel)
		rel = strings.TrimSuffix(rel, ext) + suffix + ext
	}
	return planner.resolve(root, source, rel)
}

func (s *application) savePresetResult() {
	if s.modalOpen() || s.batch != nil || s.result == nil {
		return
	}
	o := s.output
	if err := o.validate(); err != nil {
		s.reportError(err, "保存失败")
		return
	}
	if o.replacesOriginal() {
		s.requestOverwrite()
		return
	}
	result := s.result
	save := func(root string) {
		path, err := o.copyPath(result.SourcePath, root, filepath.Base(result.SourcePath), make(map[string]bool))
		if err == nil {
			err = os.MkdirAll(filepath.Dir(path), 0o755)
		}
		if err != nil {
			s.busy, s.phase = false, ""
			s.reportError(err, "保存失败")
			return
		}
		s.busy = true
		s.writeCopy(result, path)
	}
	if o.Mode != presetSaveAsk {
		save(o.Directory)
		return
	}
	s.busy, s.phase = true, "请选择输出文件夹"
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.window, Title: "选择压缩结果输出文件夹", Directory: true, CreateDirectories: true})
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.reportError(err, "保存失败")
				return
			}
			if len(paths) > 0 {
				save(paths[0])
			}
		})
	}()
}
