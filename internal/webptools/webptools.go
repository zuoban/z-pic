package webptools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxWebPBytes = 64 << 20
	maxStderr    = 32 << 10
	toolTimeout  = 2 * time.Minute
)

var errOutputLimit = errors.New("WebP 工具输出超过 64 MiB 限制")

type cachedToolState struct {
	path string
	info os.FileInfo
	sum  [sha256.Size]byte
	has  bool
}

var (
	userCacheDir = os.UserCacheDir
	toolCacheMu  sync.Mutex
	toolCache    cachedToolState
)

func Encode(ctx context.Context, data []byte, quality int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("WebP 处理已取消：%w", err)
	}
	if quality < 0 || quality > 100 {
		return nil, fmt.Errorf("WebP 质量必须在 0–100 之间")
	}
	if len(data) > maxWebPBytes {
		return nil, fmt.Errorf("WebP 输入超过 64 MiB 限制")
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, fmt.Errorf("WebP 工具仅接受 WebP 输入")
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return runCommand(ctx, path, []string{
		"-q", strconv.Itoa(quality), "-m", "4", "-alpha_q", "100",
		"-metadata", "all", "-quiet", "-o", "-", "--", "-",
	}, data, maxWebPBytes)
}

func executableFilename() string {
	if runtime.GOOS == "windows" {
		return "cwebp.exe"
	}
	return "cwebp"
}

func acquireTool() (string, func(), error) {
	asset := bundledTool()
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 cwebp；请参照 scripts/prepare-webptools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH)
	}
	if path, err := cachedToolPath(asset); err == nil {
		return path, func() {}, nil
	}
	return extractBundledTool(asset)
}

func cachedToolPath(asset []byte) (string, error) {
	toolCacheMu.Lock()
	defer toolCacheMu.Unlock()
	cacheRoot, err := userCacheDir()
	if err != nil || cacheRoot == "" {
		return "", errors.New("缓存目录不可用")
	}
	if !toolCache.has {
		toolCache.sum = sha256.Sum256(asset)
		toolCache.has = true
	}
	entryDir := filepath.Join(cacheRoot, "z-pic", "tools", fmt.Sprintf("cwebp-%x", toolCache.sum[:16]))
	binaryPath := filepath.Join(entryDir, executableFilename())
	if toolCache.path == binaryPath && toolCache.info != nil && matchesVerifiedTool(entryDir, binaryPath, int64(len(asset)), toolCache.info) {
		return binaryPath, nil
	}
	if err := ensurePrivateDir(filepath.Join(cacheRoot, "z-pic")); err != nil {
		return "", err
	}
	if err := ensurePrivateDir(filepath.Join(cacheRoot, "z-pic", "tools")); err != nil {
		return "", err
	}
	if err := ensurePrivateDir(entryDir); err != nil {
		return "", err
	}
	if info, err := verifyDiskTool(binaryPath, int64(len(asset)), toolCache.sum); err == nil {
		toolCache.path, toolCache.info = binaryPath, info
		return binaryPath, nil
	}
	info, err := stageCachedTool(entryDir, binaryPath, asset)
	if err != nil {
		return "", err
	}
	toolCache.path, toolCache.info = binaryPath, info
	return binaryPath, nil
}

func matchesVerifiedTool(entryDir, binaryPath string, expectedSize int64, verified os.FileInfo) bool {
	dirInfo, err := os.Lstat(entryDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm() != 0700 {
		return false
	}
	fileInfo, err := os.Lstat(binaryPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != expectedSize {
		return false
	}
	if runtime.GOOS != "windows" && fileInfo.Mode().Perm() != 0700 {
		return false
	}
	return os.SameFile(verified, fileInfo) && fileInfo.ModTime().Equal(verified.ModTime())
}

func ensurePrivateDir(dir string) error {
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

func verifyDiskTool(binaryPath string, expectedSize int64, expectedSum [sha256.Size]byte) (os.FileInfo, error) {
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
	return openedInfo, nil
}

func stageCachedTool(entryDir, binaryPath string, asset []byte) (os.FileInfo, error) {
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

func extractTool() (string, func(), error) {
	asset := bundledTool()
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 cwebp；请参照 scripts/prepare-webptools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH)
	}
	return extractBundledTool(asset)
}

func extractBundledTool(asset []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "zpic-webptools-")
	if err != nil {
		return "", nil, fmt.Errorf("无法创建 WebP 工具临时目录：%w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, executableFilename())
	if err := os.WriteFile(path, asset, 0700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("无法释放 cwebp 工具：%w", err)
	}
	return path, cleanup, nil
}

func runCommand(ctx context.Context, path string, args []string, data []byte, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cwebp 已取消：%w", err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{limit: limit, cancel: cancel}
	stderr := &limitedDiagnostic{limit: maxStderr}
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C"}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cwebp 已取消或超时：%w", ctx.Err())
	}
	if stdout.overflow {
		return nil, fmt.Errorf("cwebp：%w", errOutputLimit)
	}
	if err != nil {
		return nil, fmt.Errorf("cwebp 执行失败（请检查捆绑工具和系统执行权限）：%w；%s",
			err, strings.TrimSpace(stderr.buf.String()))
	}
	if stdout.buf.Len() == 0 {
		return nil, fmt.Errorf("cwebp 未生成 WebP 数据")
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	size := min(len(data), w.limit-w.buf.Len())
	_, _ = w.buf.Write(data[:size])
	if size < len(data) {
		w.overflow = true
		w.cancel()
		return size, errOutputLimit
	}
	return size, nil
}

type limitedDiagnostic struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedDiagnostic) Write(data []byte) (int, error) {
	_, _ = w.buf.Write(data[:min(len(data), w.limit-w.buf.Len())])
	return len(data), nil
}
