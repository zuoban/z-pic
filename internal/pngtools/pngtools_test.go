package pngtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"zpic/internal/toolio"
)

// A subprocess of the test executable exercises real os/exec pipes and exit
// statuses. No test override is exposed to production callers.
func TestToolProcess(t *testing.T) {
	var mode string
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "larger", "larger-42", "larger-98", "larger-99":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
		fmt.Fprint(os.Stderr, "diagnostic from child")
		switch mode {
		case "larger-42":
			os.Exit(42)
		case "larger-98":
			os.Exit(98)
		case "larger-99":
			os.Exit(99)
		}
	case "prefix":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(41)
		}
		_, _ = os.Stdout.Write(data[:len(data)/2])
	case "98", "99", "42":
		fmt.Fprint(os.Stdout, "corrupt partial PNG")
		fmt.Fprint(os.Stderr, "diagnostic from child")
		code := map[string]int{"98": 98, "99": 99, "42": 42}[mode]
		os.Exit(code)
	case "empty":
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
		time.Sleep(30 * time.Second)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxStderr*4))
		fmt.Fprint(os.Stdout, "ok")
	case "larger-wait":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
		time.Sleep(30 * time.Second)
	case "wait":
		time.Sleep(30 * time.Second)
	default:
		os.Exit(41)
	}
	os.Exit(0)
}

func helperArgs(mode string) []string {
	return []string{"-test.run=^TestToolProcess$", "--", mode}
}

func TestCommandExecution(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input := bytes.Repeat([]byte("input"), 20)
	for _, code := range []string{"98", "99"} {
		t.Run(code, func(t *testing.T) {
			out, err := runCommand(context.Background(), "pngquant", exe, helperArgs(code), input, 1024)
			if err != nil || !bytes.Equal(out, input) {
				t.Fatalf("fallback: output=%q err=%v", out, err)
			}
		})
	}
	t.Run("success", func(t *testing.T) {
		out, err := runCommand(context.Background(), "oxipng", exe, helperArgs("success"), input, 1024)
		if err != nil || !bytes.Equal(out, input) || &out[0] != &input[0] {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
	t.Run("smaller-prefix", func(t *testing.T) {
		out, err := runCommand(context.Background(), "oxipng", exe, helperArgs("prefix"), input, 1024)
		if err != nil || !bytes.Equal(out, input[:len(input)/2]) {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
	t.Run("larger-output-drains", func(t *testing.T) {
		data := bytes.Repeat(input, 2000)
		out, err := runCommand(context.Background(), "oxipng", exe, helperArgs("larger"), data, len(data)+4096)
		if err != nil || len(out) != len(data) || &out[0] != &data[0] {
			t.Fatalf("output length=%d err=%v", len(out), err)
		}
	})
	for _, code := range []string{"98", "99"} {
		t.Run("larger-"+code, func(t *testing.T) {
			out, err := runCommand(context.Background(), "pngquant", exe, helperArgs("larger-"+code), input, 8192)
			if err != nil || len(out) != len(input) || &out[0] != &input[0] {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
	}
	for _, tool := range []string{"oxipng", "pngquant"} {
		t.Run(tool+"-larger-fails", func(t *testing.T) {
			out, err := runCommand(context.Background(), tool, exe, helperArgs("larger-42"), input, 8192)
			if out != nil || err == nil || !strings.Contains(err.Error(), "diagnostic from child") {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
		t.Run(tool+"-larger-overflow", func(t *testing.T) {
			out, err := runCommand(context.Background(), tool, exe, helperArgs("larger-98"), input, 1024)
			if out != nil || !errors.Is(err, errOutputLimit) {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
	}
	t.Run("larger-output-deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		out, err := runCommand(ctx, "pngquant", exe, helperArgs("larger-wait"), input, 8192)
		if out != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
	for _, mode := range []string{"42", "98", "empty"} {
		t.Run("error-"+mode, func(t *testing.T) {
			out, err := runCommand(context.Background(), "oxipng", exe, helperArgs(mode), input, 1024)
			if err == nil || out != nil {
				t.Fatalf("expected no partial result: output=%q err=%v", out, err)
			}
			if mode == "42" && !strings.Contains(err.Error(), "diagnostic from child") {
				t.Fatal(err)
			}
		})
	}
	t.Run("overflow-kills-child", func(t *testing.T) {
		start := time.Now()
		out, err := runCommand(context.Background(), "oxipng", exe, helperArgs("overflow"), input, 128)
		if out != nil || !errors.Is(err, errOutputLimit) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", out, err, time.Since(start))
		}
	})
	t.Run("bounded-stderr-drains", func(t *testing.T) {
		out, err := runCommand(context.Background(), "oxipng", exe, helperArgs("stderr"), input, 1024)
		if err != nil || string(out) != "ok" {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(100*time.Millisecond, cancel)
		defer timer.Stop()
		defer cancel()
		start := time.Now()
		out, err := runCommand(ctx, "oxipng", exe, helperArgs("wait"), input, 1024)
		if out != nil || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", out, err, time.Since(start))
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		out, err := runCommand(ctx, "oxipng", exe, helperArgs("wait"), input, 1024)
		if out != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		out, err := runCommand(context.Background(), "oxipng", t.TempDir()+"/absent", nil, input, 1024)
		if out != nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "捆绑工具") {
			t.Fatalf("output=%q err=%v", out, err)
		}
	})
}

func TestBoundedWriters(t *testing.T) {
	canceled := false
	w := &limitedOutput{input: bytes.Repeat([]byte("x"), 1024), limit: 129, cancel: func() { canceled = true }}
	// io.Copy must not bypass Write via an accidentally promoted ReadFrom.
	_, err := io.Copy(w, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if !errors.Is(err, errOutputLimit) || w.buf.Len() != 129 || w.size != 129 || !canceled {
		t.Fatalf("len=%d size=%d canceled=%v err=%v", w.buf.Len(), w.size, canceled, err)
	}
	d := toolio.NewDiagnostic(8)
	n, err := io.Copy(d, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if n != 1024 || err != nil || len(d.String()) != 8 {
		t.Fatalf("n=%d len=%d err=%v", n, len(d.String()), err)
	}
}

func TestOutputRetainsOnlySmallerCandidates(t *testing.T) {
	input := []byte("abcdefghijkl")
	for _, scenario := range []struct {
		name   string
		chunks []string
	}{
		{"empty", nil},
		{"matching-prefix", []string{"", "abc", "", "def"}},
		{"changed-first-chunk", []string{"Z", "bc", "def"}},
		{"changed-later-chunk", []string{"abc", "dZ", "", "fgh", "ijk"}},
		{"exact-input", []string{"abc", "def", "ghi", "jkl"}},
		{"equal-changed-output", []string{"abc", "dZf", "ghi", "jkl"}},
		{"larger-matching-prefix", []string{"abc", "defghijkl", "more", ""}},
		{"larger-changed-prefix", []string{"Zbc", "defghi", "jklmore", ""}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			original := bytes.Clone(input)
			canceled := false
			output := &limitedOutput{
				input: input, limit: maxPNGBytes + 1,
				cancel: func() { canceled = true },
			}
			var expected string
			for _, chunk := range scenario.chunks {
				count, err := output.Write([]byte(chunk))
				if count != len(chunk) || err != nil || canceled {
					t.Fatalf("write count=%d canceled=%v err=%v", count, canceled, err)
				}
				expected += chunk
				if output.size != len(expected) || !bytes.Equal(input, original) {
					t.Fatalf("size=%d input=%q", output.size, input)
				}
				if len(expected) >= len(input) {
					if output.buf.Cap() != 0 {
						t.Fatal("retained output with no size saving")
					}
					continue
				}
				var retained []byte
				if output.buf.Len() == 0 {
					retained = input[:output.size]
				} else {
					retained = output.buf.Bytes()
				}
				if string(retained) != expected {
					t.Fatalf("retained=%q expected=%q", retained, expected)
				}
			}
			if len(expected) < len(input) && bytes.Equal([]byte(expected), input[:len(expected)]) && output.buf.Cap() != 0 {
				t.Fatal("copied a matching input prefix")
			}
		})
	}
}

func TestOutputLimitAfterDiscard(t *testing.T) {
	canceled := false
	output := &limitedOutput{
		input: []byte("input"), limit: maxPNGBytes + 1,
		cancel: func() { canceled = true },
	}
	chunk := make([]byte, 1<<20)
	for range 64 {
		count, err := output.Write(chunk)
		if count != len(chunk) || err != nil || canceled || output.buf.Cap() != 0 {
			t.Fatalf("count=%d size=%d canceled=%v err=%v", count, output.size, canceled, err)
		}
	}
	if output.size != maxPNGBytes {
		t.Fatalf("size=%d", output.size)
	}
	count, err := output.Write(nil)
	if count != 0 || err != nil || canceled {
		t.Fatalf("empty write count=%d canceled=%v err=%v", count, canceled, err)
	}
	count, err = output.Write(chunk[:1])
	if count != 1 || !errors.Is(err, errOutputLimit) || !canceled || !output.overflow || output.buf.Cap() != 0 {
		t.Fatalf("overflow count=%d canceled=%v err=%v", count, canceled, err)
	}
	count, err = output.Write(chunk)
	if count != 0 || !errors.Is(err, errOutputLimit) {
		t.Fatalf("write after overflow count=%d err=%v", count, err)
	}
}

func TestOutputAcrossReadBoundaries(t *testing.T) {
	input := bytes.Repeat([]byte("abcdefghijklmnop"), 256)
	for _, chunkSize := range []int{1, 17, 1024, 4096} {
		for _, mismatch := range []int{0, 2048, len(input) - 2} {
			t.Run(fmt.Sprintf("chunk=%d/mismatch=%d", chunkSize, mismatch), func(t *testing.T) {
				expected := bytes.Clone(input[:len(input)-1])
				expected[mismatch] ^= 0xff
				output := &limitedOutput{
					input: input, limit: maxPNGBytes + 1,
					cancel: func() { t.Fatal("unexpected cancellation") },
				}
				for start := 0; start < len(expected); start += chunkSize {
					chunk := expected[start:min(start+chunkSize, len(expected))]
					if count, err := output.Write(chunk); count != len(chunk) || err != nil {
						t.Fatalf("count=%d err=%v", count, err)
					}
				}
				if !bytes.Equal(output.buf.Bytes(), expected) || output.size != len(expected) {
					t.Fatalf("retained length=%d size=%d", output.buf.Len(), output.size)
				}
			})
		}
	}
}

func TestPublicCancellationAndPlatform(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, fn := range []func(context.Context, []byte) ([]byte, error){Optimize, Quantize} {
		out, err := fn(ctx, nil)
		if out != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("output=%q err=%v", out, err)
		}
	}
	if _, _, err := extractTool("absent"); err == nil || !strings.Contains(err.Error(), "未捆绑") {
		t.Fatalf("missing bundle error=%v", err)
	}
	for name, fn := range map[string]func(context.Context, []byte) ([]byte, error){"oxipng": Optimize, "pngquant": Quantize} {
		if len(bundledTool(name)) == 0 {
			if _, err := fn(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "未捆绑") {
				t.Fatalf("unsupported platform error=%v", err)
			}
		}
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestToolCacheReuseRecoveryAndFallback(test *testing.T) {
	if len(bundledTool("oxipng")) == 0 || len(bundledTool("pngquant")) == 0 {
		test.Skip("native assets unavailable for this platform")
	}
	originalDir := userCacheDir
	for _, slot := range toolSlots {
		slot.Reset()
	}
	test.Cleanup(func() {
		userCacheDir = originalDir
		for _, slot := range toolSlots {
			slot.Reset()
		}
	})

	cacheRoot := test.TempDir()
	userCacheDir = func() (string, error) { return cacheRoot, nil }

	firstPath, firstCleanup, err := acquireTool("oxipng")
	if err != nil {
		test.Fatal(err)
	}
	firstCleanup()
	firstInfo, err := os.Stat(firstPath)
	if err != nil {
		test.Fatal(err)
	}
	secondPath, secondCleanup, err := acquireTool("oxipng")
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
			path, cleanup, err := acquireTool("oxipng")
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
		path, cleanup, err := acquireTool("oxipng")
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

	// File identity, size and mtime are not an integrity proof: an in-place
	// replacement can preserve all three.
	tampered := bytes.Clone(bundledTool("oxipng"))
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(firstPath, tampered, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.Chtimes(firstPath, firstInfo.ModTime(), firstInfo.ModTime()); err != nil {
		test.Fatal(err)
	}
	verifiedPath, verifiedCleanup, err := acquireTool("oxipng")
	if err != nil {
		test.Fatal(err)
	}
	verifiedCleanup()
	verifiedData, err := os.ReadFile(verifiedPath)
	if err != nil || !bytes.Equal(verifiedData, bundledTool("oxipng")) {
		test.Fatal("same-size tampering with restored mtime bypassed verification")
	}

	if err := os.WriteFile(firstPath, []byte("corrupted"), 0700); err != nil {
		test.Fatal(err)
	}
	repairedPath, repairedCleanup, err := acquireTool("oxipng")
	if err != nil {
		test.Fatal(err)
	}
	repairedCleanup()
	repairedData, err := os.ReadFile(repairedPath)
	if err != nil || !bytes.Equal(repairedData, bundledTool("oxipng")) {
		test.Fatalf("expected corrupted cache entry to self-heal: err=%v len=%d", err, len(repairedData))
	}

	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			path, cleanup, acquireErr := acquireTool("pngquant")
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
	fallbackPath, fallbackCleanup, err := acquireTool("oxipng")
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
