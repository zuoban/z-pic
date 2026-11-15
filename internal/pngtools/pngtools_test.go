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
	"strings"
	"testing"
	"time"
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
		if err != nil || !bytes.Equal(out, input) {
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
	w := &limitedOutput{limit: 129, cancel: func() { canceled = true }}
	// io.Copy must not bypass Write via an accidentally promoted ReadFrom.
	_, err := io.Copy(w, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if !errors.Is(err, errOutputLimit) || w.buf.Len() != 129 || !canceled {
		t.Fatalf("len=%d canceled=%v err=%v", w.buf.Len(), canceled, err)
	}
	d := &limitedDiagnostic{limit: 8}
	n, err := io.Copy(d, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if n != 1024 || err != nil || d.buf.Len() != 8 {
		t.Fatalf("n=%d len=%d err=%v", n, d.buf.Len(), err)
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
