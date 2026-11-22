package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Validate existing directory aliases before reserving an output. The selected
// root itself may be an alias, but a nested alias must stay inside that root.
// This is a planning check; it does not lock directories against later changes.
func validateOutputDirectory(ctx context.Context, root, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if filepath.Clean(root) == filepath.Clean(directory) {
		return nil
	}
	resolvedRoot, err := resolveOutputDirectory(ctx, root)
	if err != nil {
		return fmt.Errorf("无法解析输出文件夹：%w", err)
	}
	resolvedDir, err := resolveOutputDirectory(ctx, directory)
	if err != nil {
		return fmt.Errorf("无法解析输出子文件夹：%w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedDir)
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("输出子文件夹指向目标文件夹以外的位置")
	}
	return nil
}

// Resolve the existing prefix while retaining yet-to-be-created directories.
// A dangling symlink is an error, rather than a missing directory to create.
func resolveOutputDirectory(ctx context.Context, directory string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", err
		}
		resolved, err := resolveOutputDirectory(ctx, parent)
		if err != nil {
			return "", err
		}
		return filepath.Join(resolved, filepath.Base(absolute)), nil
	}
	if err != nil {
		return "", err
	}
	// Resolve ancestors too, even when the final component is a directory.
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(resolved)
		if err != nil {
			return "", err
		}
	}
	if !info.IsDir() {
		return "", errors.New("路径中的父级不是文件夹")
	}
	return resolved, ctx.Err()
}
