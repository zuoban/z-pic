package pngtools

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

func BenchmarkPNGOutputBuffer(bench *testing.B) {
	for _, scenario := range []struct {
		name      string
		size      int
		identical bool
	}{
		{"Tiny1KiB", 1 << 10, false},
		{"Smaller4MiB", 4 << 20, false},
		{"Equal6MiB", 6 << 20, false},
		{"Identical6MiB", 6 << 20, true},
		{"Larger24MiB", 24 << 20, false},
	} {
		bench.Run(scenario.name, func(sub *testing.B) {
			payload := bytes.Repeat([]byte("x"), scenario.size)
			input := make([]byte, 6<<20)
			if scenario.identical {
				copy(input, payload)
			}
			sub.SetBytes(int64(len(payload)))
			sub.ReportAllocs()
			sub.ResetTimer()
			for iteration := 0; iteration < sub.N; iteration++ {
				output := &limitedOutput{input: input, limit: maxPNGBytes + 1, cancel: func() {}}
				if _, err := io.Copy(output, struct{ io.Reader }{bytes.NewReader(payload)}); err != nil {
					sub.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCommandOutputFallback6MiB(bench *testing.B) {
	executable, err := os.Executable()
	if err != nil {
		bench.Fatal(err)
	}
	input := bytes.Repeat([]byte("x"), 6<<20)
	args := helperArgs("success")
	ctx := context.Background()
	bench.SetBytes(int64(len(input)))
	bench.ReportAllocs()
	bench.ResetTimer()
	for iteration := 0; iteration < bench.N; iteration++ {
		output, err := runCommand(ctx, "oxipng", executable, args, input, maxPNGBytes)
		if err != nil || len(output) != len(input) || &output[0] != &input[0] {
			bench.Fatalf("output length=%d err=%v", len(output), err)
		}
	}
}
