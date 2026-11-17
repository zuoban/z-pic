package webptools

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSmallerOutputChunkBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		chunks    []string
		discarded bool
		want      string
	}{
		{"empty", []string{"", ""}, false, ""},
		{"smaller", []string{"ab", "", "cde"}, false, "abcde"},
		{"equal", []string{"abcd", "efgh", ""}, true, ""},
		{"larger", []string{"abcd", "efgh", "more"}, true, ""},
		{"largeFirstChunk", []string{"different and larger", "more"}, true, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := []byte("original")
			output := &limitedOutput{fallback: input, limit: 64, cancel: func() { t.Fatal("unexpected cancellation") }}
			total := 0
			for _, chunk := range scenario.chunks {
				count, err := output.Write([]byte(chunk))
				if count != len(chunk) || err != nil {
					t.Fatalf("count=%d err=%v", count, err)
				}
				total += len(chunk)
			}
			if output.size != total || output.discarded != scenario.discarded || output.buf.String() != scenario.want {
				t.Fatalf("size=%d discarded=%v data=%q", output.size, output.discarded, output.buf.String())
			}
			if output.discarded && output.buf.Cap() != 0 {
				t.Fatal("discarded output retained a candidate buffer")
			}
			if string(input) != "original" {
				t.Fatal("output mutated the fallback input")
			}
		})
	}
}

func TestSmallerOutputStillEnforcesLimit(t *testing.T) {
	canceled := false
	output := &limitedOutput{fallback: []byte("input"), limit: 128, cancel: func() { canceled = true }}
	for range 2 {
		if count, err := output.Write(make([]byte, 64)); count != 64 || err != nil || canceled {
			t.Fatalf("exact limit: count=%d err=%v canceled=%v", count, err, canceled)
		}
	}
	for range 2 {
		count, err := output.Write([]byte("more"))
		if count != 0 || !errors.Is(err, errOutputLimit) || !canceled || output.size != 128 || output.buf.Cap() != 0 {
			t.Fatalf("overflow: count=%d size=%d err=%v canceled=%v", count, output.size, err, canceled)
		}
	}
}

func TestCommandSmallerOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("input")
	invoke := func(ctx context.Context, mode string, limit int) ([]byte, error) {
		return runCommandWithFallback(ctx, executable, []string{"-test.run=^TestToolProcess$", "--", mode}, input, limit, input)
	}
	for _, scenario := range []struct {
		name     string
		mode     string
		limit    int
		want     string
		fallback bool
	}{
		{"smaller", "stderr", 5, "ok", false},
		{"equal-at-limit", "success", 5, "input", true},
		{"larger", "larger", 8192, "input", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			output, err := invoke(context.Background(), scenario.mode, scenario.limit)
			if err != nil || string(output) != scenario.want {
				t.Fatalf("output=%q err=%v", output, err)
			}
			if scenario.fallback && &output[0] != &input[0] {
				t.Fatal("fallback did not reuse the original input")
			}
		})
	}
	for _, mode := range []string{"empty", "error", "stderr-error", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now()
			output, err := invoke(context.Background(), mode, 128)
			if output != nil || err == nil || time.Since(start) > 5*time.Second {
				t.Fatalf("output=%q err=%v elapsed=%v", output, err, time.Since(start))
			}
			if mode == "error" && !strings.Contains(err.Error(), "diagnostic from child") {
				t.Fatal("fallback suppressed the process diagnostic")
			}
			if mode == "overflow" && !errors.Is(err, errOutputLimit) {
				t.Fatalf("fallback suppressed the output limit: %v", err)
			}
		})
	}
	for _, deadline := range []bool{false, true} {
		name, cause := "cancellation", context.Canceled
		ctx, cancel := context.WithCancel(context.Background())
		if deadline {
			cancel()
			name, cause = "deadline", context.DeadlineExceeded
			ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
		}
		t.Run(name, func(t *testing.T) {
			defer cancel()
			if !deadline {
				timer := time.AfterFunc(100*time.Millisecond, cancel)
				defer timer.Stop()
			}
			output, err := invoke(ctx, "overflow", 8192)
			if output != nil || !errors.Is(err, cause) {
				t.Fatalf("output=%q err=%v", output, err)
			}
		})
	}
}
