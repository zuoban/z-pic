package imageinput

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	data := []byte("image payload")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := Read(context.Background(), path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("read returned %q, %v", actual, err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, data) {
		t.Fatal("file changes affected the returned snapshot")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	actual, err = Read(context.Background(), link)
	if err != nil || string(actual) != "changed" {
		t.Fatalf("regular-file symlink returned %q, %v", actual, err)
	}
}

func TestReadFileRejectsInvalidInputs(t *testing.T) {
	for _, path := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if data, err := Read(context.Background(), path); data != nil || err == nil {
			t.Fatalf("invalid path %q returned %d bytes, %v", path, len(data), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if data, err := Read(ctx, "missing"); data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read returned %d bytes, %v", len(data), err)
	}
	path := filepath.Join(t.TempDir(), "oversized")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(MaxSize + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create oversized fixture: %v, %v", err, closeErr)
	}
	if data, err := Read(context.Background(), path); data != nil || !errors.Is(err, errTooLarge) {
		t.Fatalf("oversized read returned %d bytes, %v", len(data), err)
	}
}

func TestReadSizedHandlesFileSizeChanges(t *testing.T) {
	for _, data := range []string{"", "image payload"} {
		for _, size := range []int64{0, 3, int64(len(data)), int64(len(data) + 10)} {
			t.Run(strconv.Itoa(len(data))+"/"+strconv.FormatInt(size, 10), func(t *testing.T) {
				actual, err := readSized(context.Background(), strings.NewReader(data), size)
				if err != nil || string(actual) != data {
					t.Fatalf("size hint %d returned %q, %v", size, actual, err)
				}
				if len(actual) != cap(actual) {
					t.Fatal("snapshot must not expose spare capacity")
				}
			})
		}
	}
}

type failingReader struct {
	err error
}

func (reader failingReader) Read(buffer []byte) (int, error) {
	return 0, reader.err
}

type cancelingReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (reader cancelingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.cancel()
	return count, err
}

func TestReadSizedPropagatesErrorsAndCancellation(t *testing.T) {
	expected := errors.New("read failure")
	for _, size := range []int64{0, 10} {
		for _, source := range []io.Reader{
			failingReader{err: expected},
			io.MultiReader(strings.NewReader("payload"), failingReader{err: expected}),
		} {
			if data, err := readSized(context.Background(), source, size); data != nil || !errors.Is(err, expected) {
				t.Fatalf("read failure returned %d bytes, %v", len(data), err)
			}
		}
	}
	for _, size := range []int64{0, 6, 20} {
		ctx, cancel := context.WithCancel(context.Background())
		source := cancelingReader{reader: strings.NewReader("payload"), cancel: cancel}
		data, err := readSized(ctx, source, size)
		cancel()
		if data != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled read returned %d bytes, %v", len(data), err)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestReadSizedEnforcesActualByteLimit(t *testing.T) {
	for _, length := range []int64{MaxSize, MaxSize + 1} {
		for _, size := range []int64{MaxSize - 1, MaxSize} {
			source := io.LimitReader(zeroReader{}, length)
			data, err := readSized(context.Background(), source, size)
			if length > MaxSize {
				if data != nil || !errors.Is(err, errTooLarge) {
					t.Fatalf("growing input returned %d bytes, %v", len(data), err)
				}
			} else if err != nil || int64(len(data)) != length {
				t.Fatalf("input at limit returned %d bytes, %v", len(data), err)
			}
		}
	}
	for _, size := range []int64{-1, MaxSize + 1} {
		if data, err := readSized(context.Background(), strings.NewReader(""), size); data != nil || err == nil {
			t.Fatalf("invalid size %d returned %d bytes, %v", size, len(data), err)
		}
	}
}

func TestReadLimitEnforcesCustomLimit(t *testing.T) {
	if MaxIntermediatePNGSize != 128<<20 {
		t.Fatalf("MaxIntermediatePNGSize = %d, want %d", MaxIntermediatePNGSize, 128<<20)
	}
	path := filepath.Join(t.TempDir(), "payload")
	payload := bytes.Repeat([]byte("a"), 32)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLimit(context.Background(), path, 32)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("ReadLimit at exact limit returned %d bytes, err=%v", len(got), err)
	}
	if got, err := ReadLimit(context.Background(), path, 31); got != nil || err == nil || !strings.Contains(err.Error(), "31 字节") {
		t.Fatalf("ReadLimit above limit returned %d bytes, err=%v", len(got), err)
	}
	if got, err := ReadLimit(context.Background(), path, 0); got != nil || err == nil {
		t.Fatalf("ReadLimit with invalid limit returned %d bytes, err=%v", len(got), err)
	}
	if got, err := readSizedLimit(context.Background(), io.LimitReader(zeroReader{}, 2<<20+1), 2<<20, 2<<20); got != nil || err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatalf("readSizedLimit MiB limit returned %d bytes, err=%v", len(got), err)
	}
}
