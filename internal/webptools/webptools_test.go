package webptools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"zpic/internal/toolio"
)

func TestToolProcess(t *testing.T) {
	var mode string
	for index, arg := range os.Args {
		if arg == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
			break
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "larger":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
	case "large-output":
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 24<<20))
	case "error":
		fmt.Fprint(os.Stdout, "partial output")
		fmt.Fprint(os.Stderr, "diagnostic from child")
		os.Exit(42)
	case "empty":
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
		time.Sleep(30 * time.Second)
	case "stderr", "stderr-error":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxStderr*4))
		if mode == "stderr-error" {
			os.Exit(42)
		}
		fmt.Fprint(os.Stdout, "ok")
	case "wait":
		time.Sleep(30 * time.Second)
	default:
		os.Exit(41)
	}
	os.Exit(0)
}

func TestCommandExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(ctx context.Context, mode string, limit int) ([]byte, error) {
		return runCommand(ctx, executable, []string{"-test.run=^TestToolProcess$", "--", mode}, []byte("input"), limit)
	}
	t.Run("success", func(t *testing.T) {
		output, err := invoke(context.Background(), "success", 5)
		if err != nil || string(output) != "input" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	for _, mode := range []string{"error", "empty", "stderr-error"} {
		t.Run(mode, func(t *testing.T) {
			output, err := invoke(context.Background(), mode, 1024)
			if output != nil || err == nil {
				t.Fatalf("output=%q err=%v", output, err)
			}
			if mode == "error" && !strings.Contains(err.Error(), "diagnostic from child") {
				t.Fatal(err)
			}
			if len(err.Error()) > maxStderr+512 {
				t.Fatalf("unbounded diagnostic: %d", len(err.Error()))
			}
		})
	}
	t.Run("overflow-kills-child", func(t *testing.T) {
		start := time.Now()
		output, err := invoke(context.Background(), "overflow", 128)
		if output != nil || !errors.Is(err, errOutputLimit) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", output, err, time.Since(start))
		}
	})
	t.Run("stderr-drains", func(t *testing.T) {
		output, err := invoke(context.Background(), "stderr", 1024)
		if err != nil || string(output) != "ok" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		timer := time.AfterFunc(100*time.Millisecond, cancel)
		defer timer.Stop()
		start := time.Now()
		output, err := invoke(ctx, "wait", 1024)
		if output != nil || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", output, err, time.Since(start))
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		output, err := invoke(ctx, "wait", 1024)
		if output != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		output, err := runCommand(context.Background(), filepath.Join(t.TempDir(), "absent"), nil, nil, 1024)
		if output != nil || (!errors.Is(err, os.ErrNotExist) && !errors.Is(err, exec.ErrNotFound)) {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
}

func TestBoundedWriters(t *testing.T) {
	canceled := false
	writer := &limitedOutput{limit: 128, cancel: func() { canceled = true }}
	if count, err := writer.Write(make([]byte, 128)); count != 128 || err != nil || canceled {
		t.Fatalf("exact limit: count=%d err=%v canceled=%v", count, err, canceled)
	}
	for attempt := 0; attempt < 2; attempt++ {
		count, err := io.Copy(writer, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
		if count != 0 || !errors.Is(err, errOutputLimit) || writer.buf.Len() != 128 || !canceled {
			t.Fatalf("count=%d length=%d canceled=%v err=%v", count, writer.buf.Len(), canceled, err)
		}
	}
	diagnostic := toolio.NewDiagnostic(8)
	count, err := io.Copy(diagnostic, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if count != 1024 || err != nil || len(diagnostic.String()) != 8 {
		t.Fatalf("count=%d length=%d err=%v", count, len(diagnostic.String()), err)
	}
}

func TestInputGuards(t *testing.T) {
	for name, encode := range map[string]func(context.Context, []byte, int) ([]byte, error){
		"Encode":        Encode,
		"EncodeSmaller": EncodeSmaller,
	} {
		t.Run(name, func(t *testing.T) { testInputGuards(t, encode) })
	}
}

func testInputGuards(t *testing.T, encode func(context.Context, []byte, int) ([]byte, error)) {
	t.Helper()
	input, err := os.ReadFile("testdata/alpha-metadata.webp")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if output, err := encode(ctx, input, 75); output != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	for _, quality := range []int{-1, 101} {
		if output, err := encode(context.Background(), input, quality); output != nil || err == nil {
			t.Fatalf("quality=%d output=%q err=%v", quality, output, err)
		}
	}
	for _, data := range [][]byte{nil, []byte("PNG"), []byte("RIFFxxxxPNG ")} {
		if output, err := encode(context.Background(), data, 75); output != nil || err == nil {
			t.Fatalf("output=%q err=%v", output, err)
		}
	}
	if output, err := encode(context.Background(), make([]byte, maxWebPBytes+1), 75); output != nil || err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(bundledTool()) == 0 {
		if _, err := encode(context.Background(), input, 75); err == nil || !strings.Contains(err.Error(), "未捆绑") {
			t.Fatalf("unsupported platform error=%v", err)
		}
	}
}

func TestToolCacheReuseRecoveryAndFallback(test *testing.T) {
	if len(bundledTool()) == 0 {
		test.Skip("native asset unavailable for this platform")
	}
	originalDir := userCacheDir
	cwebpSlot.Reset()
	test.Cleanup(func() {
		userCacheDir = originalDir
		cwebpSlot.Reset()
	})

	cacheRoot := test.TempDir()
	userCacheDir = func() (string, error) { return cacheRoot, nil }

	firstPath, firstCleanup, err := acquireTool()
	if err != nil {
		test.Fatal(err)
	}
	firstCleanup()
	firstInfo, err := os.Stat(firstPath)
	if err != nil {
		test.Fatal(err)
	}
	secondPath, secondCleanup, err := acquireTool()
	if err != nil {
		test.Fatal(err)
	}
	secondCleanup()
	secondInfo, err := os.Stat(secondPath)
	if err != nil {
		test.Fatal(err)
	}
	if firstPath != secondPath || !os.SameFile(firstInfo, secondInfo) {
		test.Fatalf("expected cached executable reuse: first=%s second=%s", firstPath, secondPath)
	}
	for _, parent := range []string{filepath.Join(cacheRoot, "z-pic"), filepath.Join(cacheRoot, "z-pic", "tools")} {
		if runtime.GOOS != "windows" {
			if err := os.Chmod(parent, 0777); err != nil {
				test.Fatal(err)
			}
			path, cleanup, err := acquireTool()
			if err != nil {
				test.Fatal(err)
			}
			cleanup()
			info, err := os.Stat(parent)
			if err != nil || info.Mode().Perm() != 0700 || path != firstPath {
				test.Fatalf("cached parent permissions not repaired: path=%s info=%v err=%v", path, info, err)
			}
		}
		moved := parent + "-moved"
		if err := os.Rename(parent, moved); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink(moved, parent); err != nil {
			test.Fatal(err)
		}
		path, cleanup, err := acquireTool()
		if err != nil {
			test.Fatal(err)
		}
		cleanup()
		if path == firstPath {
			test.Fatal("reused cached executable through a symlinked parent")
		}
		if err := os.Remove(parent); err != nil {
			test.Fatal(err)
		}
		if err := os.Rename(moved, parent); err != nil {
			test.Fatal(err)
		}
	}

	tampered := bytes.Clone(bundledTool())
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(firstPath, tampered, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.Chtimes(firstPath, firstInfo.ModTime(), firstInfo.ModTime()); err != nil {
		test.Fatal(err)
	}
	verifiedPath, verifiedCleanup, err := acquireTool()
	if err != nil {
		test.Fatal(err)
	}
	verifiedCleanup()
	verifiedData, err := os.ReadFile(verifiedPath)
	if err != nil || !bytes.Equal(verifiedData, bundledTool()) {
		test.Fatal("same-size tampering with restored mtime bypassed verification")
	}

	if err := os.WriteFile(firstPath, []byte("corrupted"), 0700); err != nil {
		test.Fatal(err)
	}
	repairedPath, repairedCleanup, err := acquireTool()
	if err != nil {
		test.Fatal(err)
	}
	repairedCleanup()
	repairedData, err := os.ReadFile(repairedPath)
	if err != nil || !bytes.Equal(repairedData, bundledTool()) {
		test.Fatalf("expected corrupted cache entry to self-heal: err=%v len=%d", err, len(repairedData))
	}

	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			path, cleanup, acquireErr := acquireTool()
			if acquireErr != nil {
				errs <- acquireErr
				return
			}
			cleanup()
			if _, statErr := os.Stat(path); statErr != nil {
				errs <- statErr
			}
		}()
	}
	group.Wait()
	close(errs)
	for acquireErr := range errs {
		test.Fatal(acquireErr)
	}

	userCacheDir = func() (string, error) { return "", errors.New("unavailable") }
	fallbackPath, fallbackCleanup, err := acquireTool()
	if err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(fallbackPath); err != nil {
		test.Fatal(err)
	}
	fallbackCleanup()
	if _, err := os.Stat(fallbackPath); !os.IsNotExist(err) {
		test.Fatalf("expected fallback temporary binary removal, got err=%v", err)
	}
}
