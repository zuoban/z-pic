package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
	"zpic/internal/nameseq"
)

// resolveBatchOutputPath chooses a collision-free output file inside outputDir
// without overwriting the source path or any existing file.
func resolveBatchOutputPath(outputDir, sourcePath, relPath string, used map[string]bool) (string, error) {
	return (&batchOutputPlanner{used: used}).resolve(outputDir, sourcePath, relPath)
}

type batchOutputPlanner struct {
	ctx         context.Context
	used        map[string]bool
	sequences   map[string]batchOutputSequence
	parents     map[string]batchParentPath
	lastRoot    string
	lastRootAbs string
}

type batchOutputSequence = nameseq.Sequence

const maxBatchOutputCandidates = 10000

func (planner *batchOutputPlanner) resolve(outputDir, sourcePath, relPath string) (string, error) {
	return planner.resolveCandidate(outputDir, sourcePath, relPath, false)
}

func (planner *batchOutputPlanner) resolveCandidate(outputDir, sourcePath, relPath string, numberedOnly bool) (string, error) {
	ctx := planner.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var rootAbs string
	if outputDir != "" && outputDir == planner.lastRoot && planner.lastRootAbs != "" {
		rootAbs = planner.lastRootAbs
	} else {
		var err error
		rootAbs, err = filepath.Abs(outputDir)
		if err != nil {
			return "", fmt.Errorf("无法解析输出文件夹路径：%w", err)
		}
		planner.lastRoot, planner.lastRootAbs = outputDir, rootAbs
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("无法解析原图路径：%w", err)
	}
	safeRel := sanitizeRelPath(relPath, filepath.Base(sourcePath))
	candidate := filepath.Join(rootAbs, safeRel)
	relCheck, err := filepath.Rel(rootAbs, candidate)
	if err != nil || relCheck == "." || !filepath.IsLocal(relCheck) {
		return "", errors.New("输出路径超出目标文件夹范围")
	}
	if err := validateOutputDirectory(ctx, rootAbs, filepath.Dir(candidate)); err != nil {
		return "", err
	}
	if planner.parents == nil {
		planner.parents = make(map[string]batchParentPath)
	}
	sourceCanon := canonicalBatchKeyWithParents(sourceAbs, planner.parents)
	sourceCanonDir, sourceCanonBase := filepath.Split(sourceCanon)
	candCanonDir, _ := filepath.Split(canonicalBatchKeyWithParents(candidate, planner.parents))
	sameSourceDir := candCanonDir == sourceCanonDir
	canUse := func(path string) (bool, error) {
		if path == sourceAbs || (sameSourceDir && filepath.Base(path) == sourceCanonBase) {
			return false, nil
		}
		_, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return true, nil
		}
		return false, statErr
	}
	ext := filepath.Ext(candidate)
	stem := strings.TrimSuffix(candidate, ext)
	if planner.used == nil {
		planner.used = make(map[string]bool)
	}
	if planner.sequences == nil {
		planner.sequences = make(map[string]batchOutputSequence)
	}
	seqKey := candidate
	if numberedOnly {
		seqKey += "\x00numbered"
	}
	sequence := planner.sequences[seqKey]
	for index := 0; index < maxBatchOutputCandidates; index++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		index = sequence.Next(index)
		if index >= maxBatchOutputCandidates {
			break
		}
		path := candidate
		if numberedOnly {
			if index >= 1 {
				path = fmt.Sprintf("%s-%d%s", stem, index+1, ext)
			}
		} else if index == 1 {
			path = stem + "-compressed" + ext
		} else if index > 1 {
			path = fmt.Sprintf("%s-compressed-%d%s", stem, index, ext)
		}
		if planner.used[path] {
			if sequence == nil {
				sequence = make(batchOutputSequence)
				planner.sequences[seqKey] = sequence
			}
			sequence[index] = index + 1
			continue
		}
		ok, err := canUse(path)
		if err != nil {
			return "", fmt.Errorf("无法检查输出路径：%w", err)
		}
		if ok {
			planner.used[path] = true
			if sequence != nil {
				sequence[index] = index + 1
			}
			return path, nil
		}
	}
	return "", errors.New("同名输出文件过多，请选择其他目录或文件名")
}

func (s *application) openOutputDir() {
	if s.modalOpen() || s.batch == nil || s.batch.outputDir == "" {
		return
	}
	if err := mygo.Shell.OpenPath(s.batch.outputDir); err != nil {
		s.setMessage("无法打开输出文件夹："+err.Error(), true)
	}
}

func (s *application) openBatchPreview(index int) {
	if s.modalOpen() || s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return
	}
	item := s.batch.items[index]
	if item.invalidInput {
		s.setMessage("该文件无效，无法预览："+item.errMsg, true)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在加载图片预览", cancel
	s.setMessage("", false)
	go func() {
		selected, err := s.readSelectionWithBudget(ctx, item.path, nil)
		var result *compress.Result
		var outputPreview *ui.Bitmap
		if err == nil && item.status == batchDone {
			result = &compress.Result{
				SourcePath:   item.path,
				Format:       item.format,
				Width:        item.width,
				Height:       item.height,
				OriginalSize: item.originalSize,
				OutputSize:   item.outputSize,
				Unchanged:    item.unchanged,
			}
			if item.overwritten {
				outputPreview = selected.preview
				result.Data = selected.data
			} else if item.savedPath != "" {
				var saved *selectedImage
				saved, err = s.readSelectionWithBudget(ctx, item.savedPath, selected)
				if err == nil {
					outputPreview = saved.preview
					result.Data = saved.data
				}
			}
		}
		s.update(func() {
			defer cancel()
			s.completeBatchPreview(ctx, index, item, selected, result, outputPreview, err)
		})
	}()
}

func (s *application) completeBatchPreview(
	ctx context.Context,
	index int,
	item batchItem,
	selected *selectedImage,
	result *compress.Result,
	outputPreview *ui.Bitmap,
	err error,
) {
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "加载预览失败")
		return
	}
	if s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return
	}
	s.batch.previewOpen = true
	s.batch.previewIndex = index
	s.batch.selected = index
	s.file = selected
	s.result = result
	s.preview = selected.preview
	if outputPreview != nil {
		s.preview = outputPreview
	}
	s.overwritten = item.overwritten
	s.comparison = comparisonView{split: 50}
	if item.overwritten {
		s.comparison.mode = previewResult
	}
}

func (s *application) adjacentBatchPreviewIndex(delta int) int {
	if s.batch == nil || !s.batch.previewOpen || len(s.batch.items) == 0 {
		return -1
	}
	visible := s.filteredBatchIndices()
	if pos := slices.Index(visible, s.batch.previewIndex); pos >= 0 {
		for next := pos + delta; next >= 0 && next < len(visible); next += delta {
			idx := visible[next]
			if !s.batch.items[idx].invalidInput {
				return idx
			}
		}
		return -1
	}
	for next := s.batch.previewIndex + delta; next >= 0 && next < len(s.batch.items); next += delta {
		if !s.batch.items[next].invalidInput {
			return next
		}
	}
	return -1
}

func (s *application) stepBatchPreview(delta int) {
	if s.modalOpen() {
		return
	}
	if next := s.adjacentBatchPreviewIndex(delta); next >= 0 {
		s.openBatchPreview(next)
	}
}

func (s *application) closeBatchPreview() {
	if s.batch == nil {
		return
	}
	s.batch.previewOpen = false
	s.batch.previewIndex = -1
	s.file = nil
	s.result = nil
	s.preview = nil
	s.overwritten = false
	s.comparison = comparisonView{split: 50}
}
