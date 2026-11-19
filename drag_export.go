package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/transfer"
)

func (s *application) cleanupDragStaging() {
	if s.dragTempDir != "" {
		_ = os.RemoveAll(s.dragTempDir)
		s.dragTempDir = ""
	}
	s.dragStagedResult = nil
	s.dragStagedPath = ""
}

func (s *application) isDragStagedPath(path string) bool {
	if s.dragTempDir == "" || path == "" {
		return false
	}
	root, err := filepath.Abs(s.dragTempDir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (s *application) singleDragFilename() string {
	ext := ".jpg"
	resultFormat := ""
	if s.result != nil {
		resultFormat = s.result.Format
		if mapped := outputExtensionForFormat(resultFormat); mapped != "" {
			ext = mapped
		}
	}
	name := "compressed" + ext
	if s.file != nil && s.file.path != "" {
		base := filepath.Base(s.file.path)
		if base != "" && base != "." && base != string(filepath.Separator) {
			name = replaceFilenameWithConcreteFormat(base, s.file.format, resultFormat)
			if s.output.UseSuffix {
				suffix := s.output.Suffix
				if suffix == "" {
					suffix = defaultFilenameSuffix
				}
				fileExt := filepath.Ext(name)
				stem := strings.TrimSuffix(name, fileExt)
				name = stem + suffix + fileExt
			}
		}
	}
	return filepath.Base(name)
}

func (s *application) stageSingleDragFile() string {
	if s.result == nil || len(s.result.Data) == 0 {
		return ""
	}
	if s.lastSavedPath != "" {
		if info, err := os.Stat(s.lastSavedPath); err == nil && info.Mode().IsRegular() {
			if abs, err := filepath.Abs(s.lastSavedPath); err == nil {
				return abs
			}
		}
	}
	if s.dragStagedResult == s.result && s.dragStagedPath != "" {
		if info, err := os.Stat(s.dragStagedPath); err == nil && info.Mode().IsRegular() {
			return s.dragStagedPath
		}
	}
	if s.dragTempDir == "" {
		dir, err := os.MkdirTemp("", "zpic-drag-*")
		if err != nil {
			s.setMessage("准备拖出文件失败："+err.Error(), true)
			return ""
		}
		s.dragTempDir = dir
	}
	slot, err := os.MkdirTemp(s.dragTempDir, "export-*")
	if err != nil {
		s.setMessage("准备拖出文件失败："+err.Error(), true)
		return ""
	}
	staged := filepath.Join(slot, s.singleDragFilename())
	if err := os.WriteFile(staged, s.result.Data, 0o600); err != nil {
		_ = os.RemoveAll(slot)
		s.setMessage("写入拖出文件失败："+err.Error(), true)
		return ""
	}
	abs, err := filepath.Abs(staged)
	if err != nil {
		abs = staged
	}
	if s.dragStagedPath != "" && s.dragStagedPath != abs {
		oldSlot := filepath.Dir(s.dragStagedPath)
		if oldSlot != s.dragTempDir && s.isDragStagedPath(oldSlot) {
			_ = os.RemoveAll(oldSlot)
		}
	}
	s.dragStagedResult = s.result
	s.dragStagedPath = abs
	return abs
}

func (s *application) dragExportOptions() transfer.DragOptions {
	return transfer.DragOptions{
		Operations: transfer.Copy,
		Done: func(result transfer.Result) {
			s.draggingOut = false
			if result.Err != nil {
				s.setMessage("拖出文件失败："+result.Err.Error(), true)
			}
		},
	}
}

func (s *application) dragExportDataForPaths(paths ...string) transfer.Data {
	valid := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil || seen[abs] {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		seen[abs] = true
		valid = append(valid, abs)
	}
	if len(valid) == 0 {
		return transfer.Data{}
	}
	data, err := transfer.FileData(valid...)
	if err != nil {
		return transfer.Data{}
	}
	s.draggingOut = true
	return data
}

func (s *application) singleDragExportData() transfer.Data {
	if s.modalOpen() || s.batch != nil || s.result == nil {
		return transfer.Data{}
	}
	return s.dragExportDataForPaths(s.stageSingleDragFile())
}

func (s *application) batchItemDragExportData(index int) transfer.Data {
	if s.modalOpen() || s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return transfer.Data{}
	}
	outPath := batchItemOutputPath(s.batch.items[index])
	if outPath == "" {
		return transfer.Data{}
	}
	return s.dragExportDataForPaths(outPath)
}

func (s *application) batchDoneOutputPaths() []string {
	if s.batch == nil {
		return nil
	}
	paths := make([]string, 0, len(s.batch.items))
	seen := make(map[string]bool, len(s.batch.items))
	for _, item := range s.batch.items {
		outPath := batchItemOutputPath(item)
		if outPath == "" || seen[outPath] {
			continue
		}
		seen[outPath] = true
		paths = append(paths, outPath)
	}
	return paths
}

func (s *application) batchAllDoneDragExportData() transfer.Data {
	if s.modalOpen() || s.batch == nil {
		return transfer.Data{}
	}
	return s.dragExportDataForPaths(s.batchDoneOutputPaths()...)
}

func (s *application) floatingItemDragExportData(item batchItem) transfer.Data {
	if s.modalOpen() {
		return transfer.Data{}
	}
	if s.batch == nil && s.file != nil && s.file.path == item.path && s.result != nil {
		return s.dragExportDataForPaths(s.stageSingleDragFile())
	}
	if outPath := batchItemOutputPath(item); outPath != "" {
		return s.dragExportDataForPaths(outPath)
	}
	return transfer.Data{}
}
