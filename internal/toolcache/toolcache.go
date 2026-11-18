package toolcache

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const permissionMask = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

// ExecutableFilename returns the platform binary filename for name.
func ExecutableFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// CheckPrivateDir verifies an existing ordinary directory with 0700 permissions
// without mutating filesystem state, suitable for concurrent read-locked fast paths.
func CheckPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("缓存路径不是普通目录")
	}
	if runtime.GOOS != "windows" && info.Mode()&permissionMask != 0700 {
		return errors.New("缓存目录权限不匹配")
	}
	return nil
}

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
	if runtime.GOOS != "windows" && info.Mode()&permissionMask != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// Slot manages concurrent verification and self-healing extraction for one bundled binary.
type Slot struct {
	mu      sync.RWMutex
	digest  [sha256.Size]byte
	hasSum  bool
	sumOnce sync.Mutex
}

func (s *Slot) sum(asset []byte) [sha256.Size]byte {
	s.sumOnce.Lock()
	defer s.sumOnce.Unlock()
	if !s.hasSum {
		s.digest = sha256.Sum256(asset)
		s.hasSum = true
	}
	return s.digest
}

// Reset clears the memoized asset digest (intended for tests).
func (s *Slot) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sumOnce.Lock()
	s.digest = [sha256.Size]byte{}
	s.hasSum = false
	s.sumOnce.Unlock()
}

// Acquire verifies or stages the bundled binary under cacheDirFn, falling back
// to an isolated temporary directory if the user cache path is unavailable or untrusted.
func (s *Slot) Acquire(name, tempPrefix string, asset []byte, cacheDirFn func() (string, error)) (string, func(), error) {
	if path, err := s.cachedPath(name, asset, cacheDirFn); err == nil {
		return path, func() {}, nil
	}
	return ExtractTemp(tempPrefix, name, asset)
}

func (s *Slot) cachedPath(name string, asset []byte, cacheDirFn func() (string, error)) (string, error) {
	cacheRoot, err := cacheDirFn()
	if err != nil || cacheRoot == "" {
		return "", errors.New("缓存目录不可用")
	}
	sum := s.sum(asset)
	appDir := filepath.Join(cacheRoot, "z-pic")
	toolsDir := filepath.Join(appDir, "tools")
	entryDir := filepath.Join(toolsDir, fmt.Sprintf("%s-%x", name, sum[:16]))
	binaryPath := filepath.Join(entryDir, ExecutableFilename(name))

	// Concurrent workers verify in parallel under RLock when the cache is healthy.
	s.mu.RLock()
	fastOK := CheckPrivateDir(appDir) == nil &&
		CheckPrivateDir(toolsDir) == nil &&
		CheckPrivateDir(entryDir) == nil
	if fastOK {
		_, verifyErr := Verify(binaryPath, int64(len(asset)), sum)
		fastOK = verifyErr == nil
	}
	s.mu.RUnlock()
	if fastOK {
		return binaryPath, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := EnsurePrivateDir(appDir); err != nil {
		return "", err
	}
	if err := EnsurePrivateDir(toolsDir); err != nil {
		return "", err
	}
	if err := EnsurePrivateDir(entryDir); err != nil {
		return "", err
	}
	if _, err := Verify(binaryPath, int64(len(asset)), sum); err != nil {
		if _, err := Stage(entryDir, binaryPath, asset); err != nil {
			return "", err
		}
	}
	return binaryPath, nil
}

// ExtractTemp writes asset into a fresh 0700 temporary directory and returns its cleanup function.
func ExtractTemp(prefix, name string, asset []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, fmt.Errorf("无法创建 %s 工具临时目录：%w", name, err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, ExecutableFilename(name))
	if err := os.WriteFile(path, asset, 0700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("无法释放 %s 工具：%w", name, err)
	}
	return path, cleanup, nil
}

func Verify(binaryPath string, expectedSize int64, expectedSum [sha256.Size]byte) (os.FileInfo, error) {
	fileInfo, err := os.Lstat(binaryPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != expectedSize {
		return nil, errors.New("缓存工具不存在或大小不匹配")
	}
	if runtime.GOOS != "windows" && fileInfo.Mode()&permissionMask != 0700 {
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
	if runtime.GOOS != "windows" && info.Mode()&permissionMask != 0700 {
		return nil, errors.New("缓存工具权限不匹配")
	}
	return info, nil
}
