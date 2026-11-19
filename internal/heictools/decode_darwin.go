//go:build darwin

package heictools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zpic/internal/contextio"
	"zpic/internal/toolio"
)

const (
	toolTimeout        = 60 * time.Second
	maxDecodedPNGBytes = 128 << 20
)

var sipsBinaryPath = "/usr/bin/sips"

// Available reports whether macOS system HEIC decoding via sips is available.
func Available() bool {
	if sipsBinaryPath == "" {
		return false
	}
	info, err := os.Stat(sipsBinaryPath)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// DecodePNG decodes a validated static HEIC image to lossless PNG bytes using
// macOS ImageIO via sips. Orientation metadata (eXIf) and color profiles (iCCP)
// emitted by sips are preserved in the returned PNG stream.
func DecodePNG(ctx context.Context, data []byte) ([]byte, error) {
	return decodePNG(ctx, data, 0)
}

// DecodeThumbnailPNG decodes a validated static HEIC image to PNG bytes,
// optionally downscaling to fit within maxEdge when the source exceeds maxEdge.
func DecodeThumbnailPNG(ctx context.Context, data []byte, maxEdge int) ([]byte, error) {
	if maxEdge <= 0 {
		return nil, fmt.Errorf("缩略图尺寸必须大于零")
	}
	return decodePNG(ctx, data, maxEdge)
}

func decodePNG(ctx context.Context, data []byte, maxEdge int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("HEIC 解码已取消：%w", err)
	}
	if !Available() {
		return nil, ErrUnsupportedPlatform
	}
	info, err := InspectBytes(ctx, data)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "zpic-heic-*")
	if err != nil {
		return nil, fmt.Errorf("无法创建 HEIC 解码临时目录：%w", err)
	}
	defer os.RemoveAll(tmpDir)

	inPath := filepath.Join(tmpDir, "input.heic")
	outPath := filepath.Join(tmpDir, "output.png")
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return nil, fmt.Errorf("无法写入 HEIC 临时文件：%w", err)
	}

	args := make([]string, 0, 8)
	if maxEdge > 0 && max(info.Width, info.Height) > maxEdge {
		args = append(args, "-Z", strconv.Itoa(maxEdge))
	}
	args = append(args, "-s", "format", "png", "--out", outPath, inPath)

	runCtx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, sipsBinaryPath, args...)
	cmd.Stdout = io.Discard
	stderr := toolio.NewDiagnostic(toolio.MaxDiagnosticBytes)
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("HEIC 解码已取消：%w", ctxErr)
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("HEIC 解码超时：%w", runCtx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("HEIC 解码失败：%s", detail)
		}
		return nil, fmt.Errorf("HEIC 解码失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("HEIC 解码已取消：%w", err)
	}

	pngData, err := readBoundedFile(ctx, outPath, maxDecodedPNGBytes)
	if err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(pngData)))
	if err != nil || format != "png" {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("HEIC 解码已取消：%w", ctxErr)
		}
		return nil, fmt.Errorf("HEIC 解码输出无效：%v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || uint64(cfg.Width) > uint64(maxHEICPixels)/uint64(cfg.Height) {
		return nil, errors.New("HEIC 图片超过 3200 万像素限制或尺寸无效")
	}
	return pngData, nil
}

func readBoundedFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取 HEIC 解码结果：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("无法读取 HEIC 解码结果信息：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("HEIC 解码结果为空")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("HEIC 解码结果超过大小限制")
	}
	buf := make([]byte, 0, info.Size())
	out := bytes.NewBuffer(buf)
	n, err := io.Copy(out, io.LimitReader(contextio.NewReader(ctx, f), limit+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("HEIC 解码已取消：%w", ctxErr)
		}
		return nil, fmt.Errorf("读取 HEIC 解码结果失败：%w", err)
	}
	if n > limit {
		return nil, fmt.Errorf("HEIC 解码结果超过大小限制")
	}
	return out.Bytes(), nil
}
