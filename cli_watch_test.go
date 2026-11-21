package main

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zpic/internal/compress"
)

func TestCLIWatchRejectsInputAsOutput(t *testing.T) {
	in := t.TempDir()
	if !shouldRunCLI([]string{"watch", in}) {
		t.Fatal("watch must dispatch without starting the desktop app")
	}
	check := func(out string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var stderr bytes.Buffer
		if code := runCLIContext(ctx, []string{"watch", "--output-dir", out, in}, io.Discard, &stderr); code != 2 || stderr.Len() == 0 {
			t.Fatalf("watch accepted output alias %q: code=%d, stderr=%s", out, code, &stderr)
		}
	}
	check(in)
	check(filepath.Join(in, "nested-output"))
	t.Run("symlink", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(in, alias); err != nil {
			t.Skipf("cannot create an input alias: %v", err)
		}
		check(alias)
		check(filepath.Join(alias, "nested-output"))
	})
}

func TestCLIWatchNewImages(t *testing.T) {
	root := t.TempDir()
	in, out := filepath.Join(root, "screenshots"), filepath.Join(root, "compressed")
	originals := make(map[string][]byte)
	existing := filepath.Join(in, "existing.jpg")
	originals[existing] = writeTestJPEG(t, existing, 320, 160, 100)
	staged := filepath.Join(root, "incoming.jpg")
	data := writeTestJPEG(t, staged, 320, 160, 100)
	settingsPath := filepath.Join(root, "settings.json")
	if err := saveFullSettings(settingsPath, appSettings{
		presets:  []compressionPreset{{name: "Screenshots", quality: 65, png: compress.DefaultPNGOptions(), maxWidth: 160}},
		selected: "Screenshots",
		theme:    themeSystem,
	}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{out, filepath.Join(in, "nested")} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		args := []string{"watch", in, "--output-dir", out, "--settings", settingsPath, "--preset", "Screenshots", "--max-size", "20KiB"}
		done <- runCLIContext(ctx, args, &watchReadyWriter{Writer: &stdout, ready: ready}, &stderr)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch did not stop after cancellation")
		}
	})
	select {
	case <-ready:
	case code := <-done:
		done <- code
		t.Fatalf("watch exited at startup: code=%d, stdout=%s, stderr=%s", code, &stdout, &stderr)
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not announce readiness")
	}

	put := func(path string, contents []byte) {
		t.Helper()
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		originals[path] = contents
	}
	put(filepath.Join(in, "nested", "ignored.jpg"), data)
	put(filepath.Join(in, "notes.txt"), []byte("ignore"))
	put(filepath.Join(in, "invalid.jpg"), []byte("invalid JPEG"))
	first := filepath.Join(in, "first.jpg")
	if err := os.Rename(staged, first); err != nil {
		t.Fatal(err)
	}
	originals[first] = data
	put(filepath.Join(in, "second.jpg"), data)

	slow := filepath.Join(in, "slow.jpg")
	file, err := os.OpenFile(slow, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(data[:8]); err != nil {
		t.Fatal(err)
	}
	waitForWatchCopies(t, out, []string{"first", "second"})
	if _, err := file.Write(data[8:]); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	originals[slow] = data

	copies := waitForWatchCopies(t, out, []string{"first", "second", "slow"})
	cancel()
	select {
	case code := <-done:
		done <- code
		if code != 0 && code != 130 {
			t.Fatalf("watch exited with %d after cancellation\nstdout: %s\nstderr: %s", code, &stdout, &stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop after cancellation")
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != len(copies) {
		t.Fatalf("watch left extra output files: %v, %v", entries, err)
	}
	for _, copy := range copies {
		path := filepath.Join(out, copy.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := jpeg.DecodeConfig(bytes.NewReader(contents))
		if err != nil || info.Width != 160 || info.Height != 80 || len(contents) > 20<<10 {
			t.Errorf("%s did not honor preset and target size: %+v, %d bytes, %v", copy.Name(), info, len(contents), err)
		}
	}
	for path, original := range originals {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
			t.Errorf("source %s changed: %v", path, err)
		}
	}
}

type watchReadyWriter struct {
	io.Writer
	ready chan struct{}
}

func (w *watchReadyWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if w.ready != nil {
		close(w.ready)
		w.ready = nil
	}
	return n, err
}

func waitForWatchCopies(t *testing.T, dir string, stems []string) []os.DirEntry {
	t.Helper()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var copies []os.DirEntry
		seen := make(map[string]bool)
		for _, entry := range entries {
			if ext := strings.ToLower(filepath.Ext(entry.Name())); ext != ".jpg" && ext != ".jpeg" {
				continue
			}
			matched := false
			for _, stem := range stems {
				if strings.HasPrefix(entry.Name(), stem+".") || strings.HasPrefix(entry.Name(), stem+"-") {
					if seen[stem] {
						t.Fatalf("watch produced duplicate copies for %s: %v", stem, entries)
					}
					seen[stem] = true
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("watch produced an unexpected copy: %s", entry.Name())
			}
			copies = append(copies, entry)
		}
		if len(copies) == len(stems) {
			return copies
		}
		select {
		case <-deadline.C:
			t.Fatalf("waiting for %v: only found %v", stems, entries)
		case <-tick.C:
		}
	}
}
