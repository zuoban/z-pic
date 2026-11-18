package toolcache

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"runtime"
)

func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("缓存路径不是普通目录")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func Verify(binaryPath string, expectedSize int64, expectedSum [sha256.Size]byte) (os.FileInfo, error) {
	fileInfo, err := os.Lstat(binaryPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != expectedSize {
		return nil, errors.New("缓存工具不存在或大小不匹配")
	}
	if runtime.GOOS != "windows" && fileInfo.Mode().Perm() != 0700 {
		return nil, errors.New("缓存工具权限无效")
	}
	file, err := os.Open(binaryPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(fileInfo, openedInfo) {
		return nil, errors.New("缓存工具身份不匹配")
	}
	hash := sha256.New()
	copied, err := io.Copy(hash, io.LimitReader(file, expectedSize+1))
	if err != nil || copied != expectedSize {
		return nil, errors.New("读取缓存工具失败")
	}
	var actualSum [sha256.Size]byte
	copy(actualSum[:], hash.Sum(nil))
	if actualSum != expectedSum {
		return nil, errors.New("缓存工具校验和不匹配")
	}
	current, err := os.Lstat(binaryPath)
	if err != nil || !os.SameFile(openedInfo, current) ||
		current.Mode() != openedInfo.Mode() || current.Size() != openedInfo.Size() ||
		!current.ModTime().Equal(openedInfo.ModTime()) {
		return nil, errors.New("校验期间缓存工具已变更")
	}
	return openedInfo, nil
}

func Stage(entryDir, binaryPath string, asset []byte) (os.FileInfo, error) {
	tempFile, err := os.CreateTemp(entryDir, ".stage-*")
	if err != nil {
		return nil, err
	}
	tempName := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempName)
	}()
	written, err := tempFile.Write(asset)
	if err != nil {
		return nil, err
	}
	if written != len(asset) {
		return nil, io.ErrShortWrite
	}
	if err := tempFile.Chmod(0700); err != nil {
		return nil, err
	}
	if err := tempFile.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tempName, binaryPath); err != nil {
		return nil, err
	}
	info, err := os.Lstat(binaryPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("缓存工具替换失败")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		return nil, errors.New("缓存工具权限不匹配")
	}
	return info, nil
}
