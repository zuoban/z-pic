package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"zpic/internal/compress"
)

type batchParentPath struct {
	path string
	info os.FileInfo
}

func (s *application) loadStartupPaths(paths []string) {
	s.openPaths(paths)
}

func (s *application) loadBatch(paths []string, appendMode bool) {
	if s.modalOpen() || len(paths) == 0 {
		return
	}
	var existing map[string]bool
	var existingDirs map[string]bool
	existingCount := 0
	if appendMode && s.batch != nil {
		existing = make(map[string]bool, len(s.batch.items))
		existingDirs = make(map[string]bool)
		for _, item := range s.batch.items {
			existing[item.absPath] = true
			if item.sourceDir != "" {
				existingDirs[item.sourceDir] = true
			}
		}
		existingCount = len(s.batch.items)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在扫描图片文件", cancel
	s.setMessage("", false)
	go func() {
		scan, err := scanBatchPathsWithDirs(ctx, paths, existing, existingDirs, existingCount)
		s.update(func() {
			defer cancel()
			s.completeBatchScan(ctx, scan, appendMode, err)
		})
	}()
}

func (s *application) completeBatchScan(ctx context.Context, scan batchScanResult, appendMode bool, err error) {
	defer s.completeDropScan()
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "扫描图片失败")
		return
	}
	if len(scan.items) == 0 {
		if scan.truncated {
			s.setMessage(batchScanMessage(0, scan.duplicates, true, appendMode), false)
		} else if appendMode && scan.duplicates > 0 {
			s.setMessage("所选图片均已在批量队列中。", false)
		} else {
			s.setMessage("未找到可处理的 JPEG、PNG 或 WebP 图片。", true)
		}
		return
	}
	if appendMode && s.batch != nil {
		s.closeBatchPreview()
		if len(scan.items) > 0 && hasMultipleBatchDirs(s.batch.items, scan.items) && !batchHasSavedOrDone(s.batch.items) {
			promoteBatchFolderPrefixes(s.batch.items)
		}
		if len(scan.items) > 0 {
			if s.batch.filter != batchFilterAll &&
				slices.ContainsFunc(scan.items, func(item batchItem) bool {
					return !matchesBatchFilter(item, s.batch.filter, "")
				}) {
				s.batch.filter = batchFilterAll
			}
			if strings.TrimSpace(s.batch.query) != "" {
				s.batch.query = ""
			}
		}
		s.batch.items = append(s.batch.items, scan.items...)
		if s.batch.selected < 0 {
			s.batch.selected = 0
		}
	} else {
		s.file, s.result, s.preview = nil, nil, nil
		s.lastSavedPath = ""
		s.comparison = comparisonView{split: 50}
		s.confirmOverwrite, s.overwritten = false, false
		s.batch = newBatchState(scan.items)
		s.configureBatchPresetOutput()
	}
	s.setMessage(batchScanMessage(len(scan.items), scan.duplicates, scan.truncated, appendMode), false)
}

func batchScanMessage(added, duplicates int, truncated, appendMode bool) string {
	verb := "已加载"
	if appendMode {
		verb = "已加入"
	}
	msg := fmt.Sprintf("%s %d 张图片。", verb, added)
	if duplicates > 0 {
		msg += fmt.Sprintf("已忽略 %d 张重复项。", duplicates)
	}
	if truncated {
		msg += fmt.Sprintf("单次最多处理 %d 张，其余文件未加入。", maxBatchFiles)
	}
	return msg
}

func scanBatchPaths(ctx context.Context, inputs []string, existing map[string]bool, existingCount int) (batchScanResult, error) {
	return scanBatchPathsWithDirs(ctx, inputs, existing, nil, existingCount)
}

func scanBatchPathsWithDirs(ctx context.Context, inputs []string, existing, existingDirs map[string]bool, existingCount int) (batchScanResult, error) {
	var out batchScanResult
	if err := ctx.Err(); err != nil {
		return out, err
	}
	seen := make(map[string]bool, len(existing))
	maps.Copy(seen, existing)
	resolvedParents := make(map[string]batchParentPath)

	uniqueDirs := make(map[string]bool, len(existingDirs))
	maps.Copy(uniqueDirs, existingDirs)
	directoryRoots := make([]string, len(inputs))
	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if info, err := os.Stat(input); err == nil && info.IsDir() {
			// Follow the directory the user explicitly selected. WalkDir
			// itself does not follow a symlink at its root.
			resolved, err := filepath.EvalSymlinks(input)
			if err != nil {
				return out, err
			}
			directoryRoots[i] = resolved
			uniqueDirs[canonicalBatchKeyWithParents(resolved, resolvedParents)] = true
		}
	}
	prefixRoot := len(uniqueDirs) > 1

	addFile := func(path, relPath, sourceDir, sourceBase string, prefixedDir, explicit bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		absPath := canonicalBatchKeyWithParents(path, resolvedParents)
		if seen[absPath] {
			out.duplicates++
			return nil
		}
		if existingCount+len(out.items) >= maxBatchFiles {
			out.truncated = true
			return errBatchLimit
		}
		seen[absPath] = true
		relPath = sanitizeRelPath(relPath, filepath.Base(path))
		info, err := compress.Inspect(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !explicit && !isSupportedImageExt(path) {
				return nil
			}
			out.items = append(out.items, batchItem{
				path:         path,
				absPath:      absPath,
				relPath:      relPath,
				sourceDir:    sourceDir,
				sourceBase:   sourceBase,
				prefixedDir:  prefixedDir,
				status:       batchFailed,
				invalidInput: true,
				errMsg:       err.Error(),
			})
			return nil
		}
		out.items = append(out.items, batchItem{
			path:         path,
			absPath:      absPath,
			relPath:      relPath,
			sourceDir:    sourceDir,
			sourceBase:   sourceBase,
			prefixedDir:  prefixedDir,
			format:       info.Format,
			width:        info.Width,
			height:       info.Height,
			originalSize: info.Size,
			status:       batchPending,
		})
		return nil
	}

	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if directoryRoots[i] == "" {
			if err := addFile(input, filepath.Base(input), "", "", false, true); err != nil {
				if errors.Is(err, errBatchLimit) {
					return out, nil
				}
				return out, err
			}
			continue
		}
		rootClean := filepath.Clean(input)
		walkRoot := directoryRoots[i]
		canonDir := canonicalBatchKeyWithParents(walkRoot, resolvedParents)
		rootBase := dirBaseName(rootClean)
		err := filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if p == walkRoot {
					return walkErr
				}
				return nil
			}
			name := d.Name()
			if p != walkRoot && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !isSupportedImageExt(name) {
				return nil
			}
			rel, relErr := filepath.Rel(walkRoot, p)
			if relErr != nil {
				rel = name
			}
			path := filepath.Join(rootClean, rel)
			if prefixRoot {
				rel = filepath.Join(rootBase, rel)
			}
			return addFile(path, rel, canonDir, rootBase, prefixRoot, false)
		})
		if err != nil {
			if errors.Is(err, errBatchLimit) {
				return out, nil
			}
			return out, err
		}
	}
	return out, nil
}

func dirBaseName(rootClean string) string {
	base := filepath.Base(rootClean)
	if base != "." && base != string(filepath.Separator) && base != "" {
		return base
	}
	if abs, err := filepath.Abs(rootClean); err == nil {
		base = filepath.Base(abs)
		if base != "." && base != string(filepath.Separator) && base != "" {
			return base
		}
	}
	return "folder"
}

func hasMultipleBatchDirs(existing, added []batchItem) bool {
	dirs := make(map[string]bool)
	for _, item := range existing {
		if item.sourceDir != "" {
			dirs[item.sourceDir] = true
		}
	}
	for _, item := range added {
		if item.sourceDir != "" {
			dirs[item.sourceDir] = true
			if len(dirs) > 1 {
				return true
			}
		}
	}
	return false
}

func batchHasSavedOrDone(items []batchItem) bool {
	return slices.ContainsFunc(items, func(item batchItem) bool {
		return item.status == batchDone || item.savedPath != ""
	})
}

func promoteBatchFolderPrefixes(items []batchItem) {
	for i := range items {
		item := &items[i]
		if item.sourceDir == "" || item.prefixedDir {
			continue
		}
		base := item.sourceBase
		if base == "" {
			base = dirBaseName(item.sourceDir)
		}
		item.relPath = sanitizeRelPath(filepath.Join(base, item.relPath), item.relPath)
		item.prefixedDir = true
	}
}

func canonicalBatchKey(path string) string {
	return canonicalBatchKeyWithParents(path, nil)
}

func canonicalBatchKeyWithParents(path string, resolvedParents map[string]batchParentPath) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	// Resolve symlinks on the parent directory (e.g. /tmp -> /private/tmp on macOS)
	// while keeping the file leaf intact so symlinked files stay identifiable.
	parent := filepath.Dir(abs)
	dir, ok := canonicalDirWithParents(parent, resolvedParents)
	if !ok {
		return filepath.Clean(abs)
	}
	return filepath.Join(dir, filepath.Base(abs))
}

func canonicalDirWithParents(dir string, resolvedParents map[string]batchParentPath) (string, bool) {
	if resolvedParents == nil {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", false
		}
		return resolved, true
	}
	if cached, found := resolvedParents[dir]; found {
		current, currentErr := os.Stat(dir)
		resolved, resolvedErr := os.Lstat(cached.path)
		if currentErr == nil && resolvedErr == nil &&
			os.SameFile(cached.info, current) && os.SameFile(current, resolved) {
			return cached.path, true
		}
		delete(resolvedParents, dir)
	}
	// Ordinary non-symlink directories inherit their parent's canonical path,
	// avoiding full root-to-leaf EvalSymlinks walks across sibling subdirectories.
	parent := filepath.Dir(dir)
	if parent != dir {
		if linfo, err := os.Lstat(dir); err == nil && linfo.IsDir() && linfo.Mode()&os.ModeSymlink == 0 {
			if parentCanon, ok := canonicalDirWithParents(parent, resolvedParents); ok {
				joined := filepath.Join(parentCanon, filepath.Base(dir))
				if resolvedInfo, rErr := os.Lstat(joined); rErr == nil && os.SameFile(linfo, resolvedInfo) {
					resolvedParents[dir] = batchParentPath{path: joined, info: linfo}
					return joined, true
				}
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(resolved); err == nil {
		resolvedParents[dir] = batchParentPath{path: resolved, info: info}
	}
	return resolved, true
}

func sanitizeRelPath(rel, fallback string) string {
	cleaned := filepath.Clean(rel)
	if cleaned == "." || !filepath.IsLocal(cleaned) {
		return filepath.Base(fallback)
	}
	return cleaned
}

func isSupportedImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}
