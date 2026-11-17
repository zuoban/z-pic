package webptools

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

func BenchmarkWebPOutputBuffer(bench *testing.B) {
	for _, scenario := range []struct {
		name string
		size int
	}{
		{"Tiny1KiB", 1 << 10},
		{"Smaller4MiB", 4 << 20},
		{"Equal6MiB", 6 << 20},
		{"Larger24MiB", 24 << 20},
	} {
		bench.Run(scenario.name, func(sub *testing.B) {
			input := make([]byte, 6<<20)
			payload := bytes.Repeat([]byte("x"), scenario.size)
			for _, mode := range []struct {
				name     string
				fallback []byte
			}{
				{"RetainAll", nil},
				{"SmallerOnly", input},
			} {
				sub.Run(mode.name, func(sample *testing.B) {
					sample.SetBytes(int64(len(payload)))
					sample.ReportAllocs()
					sample.ResetTimer()
					for iteration := 0; iteration < sample.N; iteration++ {
						output := &limitedOutput{limit: maxWebPBytes, cancel: func() {}, fallback: mode.fallback}
						if _, err := io.Copy(output, struct{ io.Reader }{bytes.NewReader(payload)}); err != nil {
							sample.Fatal(err)
						}
						if output.size != len(payload) {
							sample.Fatal("output was not fully consumed")
						}
					}
				})
			}
		})
	}
}

func BenchmarkCommandLargerOutput24MiB(bench *testing.B) {
	executable, err := os.Executable()
	if err != nil {
		bench.Fatal(err)
	}
	input := make([]byte, 6<<20)
	args := []string{"-test.run=^TestToolProcess$", "--", "large-output"}
	ctx := context.Background()
	for _, mode := range []struct {
		name     string
		fallback []byte
	}{
		{"RetainAll", nil},
		{"SmallerOnly", input},
	} {
		bench.Run(mode.name, func(sub *testing.B) {
			sub.SetBytes(24 << 20)
			sub.ReportAllocs()
			sub.ResetTimer()
			for iteration := 0; iteration < sub.N; iteration++ {
				output, err := runCommandWithFallback(ctx, executable, args, input, maxWebPBytes, mode.fallback)
				if err != nil {
					sub.Fatal(err)
				}
				if mode.fallback == nil {
					if len(output) != 24<<20 {
						sub.Fatal("output was truncated")
					}
				} else if len(output) != len(input) || &output[0] != &input[0] {
					sub.Fatal("fallback did not reuse the input")
				}
			}
		})
	}
}
