package compress

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// A non-nil snapshot proves that the result came through File/Bytes. Capture
// the input bytes themselves, never the current path contents: Bytes may be
// called after that path has already changed or disappeared.
type overwriteSnapshot struct {
	inputHash  [sha256.Size]byte
	outputHash [sha256.Size]byte
	inputSize  int64
	outputSize int64
}

func captureOverwriteSnapshot(input, output []byte) *overwriteSnapshot {
	return &overwriteSnapshot{
		inputHash: sha256.Sum256(input), outputHash: sha256.Sum256(output),
		inputSize: int64(len(input)), outputSize: int64(len(output)),
	}
}

// Overwrite atomically replaces the original source with a smaller, validated
// File/Bytes result, provided the source still matches the compression input.
// It preserves file permission bits, but does not preserve inode identity,
// ownership, extended attributes, or hard-link relationships.
//
// Portable Rename is not a conditional exchange: another process can still
// replace or remove the source between the final identity/hash check and
// Rename. These checks narrow that residual race; they do not eliminate it.
// Callers must not mutate a Result concurrently with this operation.
func (r *Result) Overwrite() error {
	if r == nil || r.snapshot == nil || r.sourcePath == "" {
		return fmt.Errorf("没有可覆盖原图的有效压缩结果")
	}
	snapshot := r.snapshot
	if r.Unchanged || snapshot.outputSize <= 0 || snapshot.outputSize >= snapshot.inputSize {
		return fmt.Errorf("压缩结果未减小，不能覆盖原图")
	}
	if int64(len(r.Data)) != snapshot.outputSize {
		return fmt.Errorf("压缩结果已变更，不能覆盖原图")
	}
	// Bind the bytes being written to the validated result, even if exported
	// fields were subsequently edited. Freeze this call's output before I/O.
	data := bytes.Clone(r.Data)
	if sha256.Sum256(data) != snapshot.outputHash {
		return fmt.Errorf("压缩结果已变更，不能覆盖原图")
	}
	source, err := verifyOverwriteSource(r.sourcePath, snapshot, nil)
	if err != nil {
		return err
	}
	return replaceOverwriteSource(r.sourcePath, data, snapshot, source)
}

// Stage the complete output before rechecking the already observed source.
func replaceOverwriteSource(path string, data []byte, snapshot *overwriteSnapshot, source os.FileInfo) error {
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("无法读取原图目录：%w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0222 == 0 {
		return fmt.Errorf("原图目录不可写，不能覆盖原图")
	}
	temp, err := os.CreateTemp(dir, ".zpic-overwrite-*")
	if err != nil {
		return fmt.Errorf("无法创建覆盖临时文件：%w", err)
	}
	defer func() {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
	}()
	n, err := temp.Write(data)
	if err != nil {
		return fmt.Errorf("写入覆盖临时文件失败：%w", err)
	}
	if n != len(data) {
		return fmt.Errorf("写入覆盖临时文件失败：%w", io.ErrShortWrite)
	}
	mode := source.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if err := temp.Chmod(mode); err != nil {
		return fmt.Errorf("保留原图权限失败：%w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("同步覆盖临时文件失败：%w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭覆盖临时文件失败：%w", err)
	}
	if _, err := verifyOverwriteSource(path, snapshot, source); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("原子替换原图失败：%w", err)
	}
	return nil
}

// Hash at most maxInputSize+1 bytes, without loading another image into memory.
// Lstat rejects final symlinks, and SameFile checks bind the opened descriptor
// and final path to the identity observed before opening.
func verifyOverwriteSource(path string, snapshot *overwriteSnapshot, previous os.FileInfo) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取原图，文件可能已删除：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("原图必须是普通文件，不能覆盖符号链接或目录")
	}
	if info.Mode().Perm()&0222 == 0 {
		return nil, fmt.Errorf("原图为只读文件，不能覆盖")
	}
	if info.Size() > maxInputSize {
		return nil, fmt.Errorf("原图超过 64 MiB 限制，不能覆盖")
	}
	if info.Size() != snapshot.inputSize {
		return nil, fmt.Errorf("原图内容已变更，请重新选择图片后再压缩")
	}
	if previous != nil && (!os.SameFile(previous, info) ||
		previous.Mode() != info.Mode() || !previous.ModTime().Equal(info.ModTime())) {
		return nil, fmt.Errorf("原图身份或属性已变更，请重新选择图片后再压缩")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开原图校验：%w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("无法读取原图校验信息：%w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("原图身份已变更，请重新选择图片后再压缩")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, maxInputSize+1))
	if err != nil {
		return nil, fmt.Errorf("校验原图内容失败：%w", err)
	}
	if n > maxInputSize {
		return nil, fmt.Errorf("原图超过 64 MiB 限制，不能覆盖")
	}
	if n != snapshot.inputSize || !bytes.Equal(hash.Sum(nil), snapshot.inputHash[:]) {
		return nil, fmt.Errorf("原图内容已变更，请重新选择图片后再压缩")
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("无法再次读取原图：%w", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) ||
		current.Mode() != info.Mode() || current.Size() != info.Size() ||
		!current.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("校验期间原图已变更，请重新选择图片后再压缩")
	}
	return current, nil
}
