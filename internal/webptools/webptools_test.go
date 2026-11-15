package webptools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
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
		output, err := runCommand(context.Background(), t.TempDir()+"/absent", nil, nil, 1024)
		if output != nil || !errors.Is(err, os.ErrNotExist) {
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
	diagnostic := &limitedDiagnostic{limit: 8}
	count, err := io.Copy(diagnostic, struct{ io.Reader }{bytes.NewReader(make([]byte, 1024))})
	if count != 1024 || err != nil || diagnostic.buf.Len() != 8 {
		t.Fatalf("count=%d length=%d err=%v", count, diagnostic.buf.Len(), err)
	}
}

func TestInputGuards(t *testing.T) {
	input, err := os.ReadFile("testdata/alpha-metadata.webp")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if output, err := Encode(ctx, input, 75); output != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	for _, quality := range []int{-1, 101} {
		if output, err := Encode(context.Background(), input, quality); output != nil || err == nil {
			t.Fatalf("quality=%d output=%q err=%v", quality, output, err)
		}
	}
	for _, data := range [][]byte{nil, []byte("PNG"), []byte("RIFFxxxxPNG ")} {
		if output, err := Encode(context.Background(), data, 75); output != nil || err == nil {
			t.Fatalf("output=%q err=%v", output, err)
		}
	}
	if output, err := Encode(context.Background(), make([]byte, maxWebPBytes+1), 75); output != nil || err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(bundledTool()) == 0 {
		if _, err := Encode(context.Background(), input, 75); err == nil || !strings.Contains(err.Error(), "未捆绑") {
			t.Fatalf("unsupported platform error=%v", err)
		}
	}
}
