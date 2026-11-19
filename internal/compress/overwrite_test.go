package compress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func assertNoOverwriteTemps(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".zpic-overwrite-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary files leaked: %v, %v", paths, err)
	}
}

func assertOverwriteFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("unexpected contents of %s: %v", path, err)
	}
}

func TestOverwriteJPEGFileAndBytes(t *testing.T) {
	data := jpegFixture(t, 100)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			path := writeFixture(t, "source.jpg", data)
			initialMode := os.FileMode(0o640)
			if runtime.GOOS != "windows" {
				if err := os.Chmod(path, initialMode|os.ModeSticky); err == nil {
					initialMode |= os.ModeSticky
				} else if err := os.Chmod(path, initialMode); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(path, initialMode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := method.run(context.Background(), path, Options{Quality: 35})
			if err != nil {
				t.Fatal(err)
			}
			if result.Unchanged || len(result.Data) >= len(data) {
				t.Fatal("fixture must save bytes")
			}
			if result.snapshot == nil || result.snapshot.inputHash != sha256.Sum256(data) {
				t.Fatal("original input snapshot was not captured")
			}
			if err := result.Save(path); err == nil {
				t.Fatal("Save allowed overwriting the source")
			}
			if err := result.Overwrite(); err != nil {
				t.Fatal(err)
			}
			assertOverwriteFile(t, path, result.Data)
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" {
				if after.Mode().Perm() != 0o640 {
					t.Fatalf("permissions changed to %v", after.Mode().Perm())
				}
				if after.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
					t.Fatalf("special mode bits leaked onto overwritten output: %v", after.Mode())
				}
			}
			if runtime.GOOS != "windows" && os.SameFile(before, after) {
				t.Fatal("source was not replaced with a new file")
			}
			img, err := jpeg.Decode(bytes.NewReader(result.Data))
			if err != nil || img.Bounds().Dx() != result.Width || img.Bounds().Dy() != result.Height {
				t.Fatalf("invalid overwritten JPEG: %v", err)
			}
			if err := result.Overwrite(); err == nil {
				t.Fatal("old result overwrote the source twice")
			}
			assertOverwriteFile(t, path, result.Data)
			// The existing result is still usable for Save As after Overwrite.
			saved := filepath.Join(filepath.Dir(path), "copy.jpg")
			if err := result.Save(saved); err != nil {
				t.Fatal(err)
			}
			assertOverwriteFile(t, saved, result.Data)
			if err := result.Save(saved); err == nil {
				t.Fatal("Save overwrote an existing output")
			}
			if err := result.Save(path); err == nil {
				t.Fatal("Save source protection regressed after Overwrite")
			}
			assertNoOverwriteTemps(t, filepath.Dir(path))
		})
	}
}

func TestOverwriteRejectsChangedSource(t *testing.T) {
	data := jpegFixture(t, 100)
	for _, method := range inputMethods(data) {
		for _, change := range []string{"same-size", "different-size", "deleted", "directory", "symlink", "readonly-file", "readonly-directory", "oversized"} {
			t.Run(method.name+"/"+change, func(t *testing.T) {
				if runtime.GOOS == "windows" && strings.HasPrefix(change, "readonly") {
					t.Skip("Unix permission bits")
				}
				path := writeFixture(t, "source.jpg", data)
				dir := filepath.Dir(path)
				result, err := method.run(context.Background(), path, Options{Quality: 35})
				if err != nil {
					t.Fatal(err)
				}
				want := bytes.Clone(data)
				switch change {
				case "same-size":
					want[len(want)-1] ^= 1
					if err := os.WriteFile(path, want, 0600); err != nil {
						t.Fatal(err)
					}
				case "different-size":
					want = append(want, 0)
					if err := os.WriteFile(path, want, 0600); err != nil {
						t.Fatal(err)
					}
				case "deleted", "directory", "symlink":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if change == "directory" {
						if err := os.Mkdir(path, 0700); err != nil {
							t.Fatal(err)
						}
					}
					if change == "symlink" {
						target := filepath.Join(dir, "target.jpg")
						if err := os.WriteFile(target, data, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(target, path); err != nil {
							t.Fatal(err)
						}
					}
				case "readonly-file":
					if err := os.Chmod(path, 0400); err != nil {
						t.Fatal(err)
					}
				case "readonly-directory":
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				case "oversized":
					if err := os.Truncate(path, maxInputSize+1); err != nil {
						t.Fatal(err)
					}
				}
				err = result.Overwrite()
				if err == nil {
					t.Fatal("changed/protected source was overwritten")
				}
				switch change {
				case "deleted":
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("missing source was recreated: %v", err)
					}
				case "directory":
					info, err := os.Lstat(path)
					if err != nil || !info.IsDir() {
						t.Fatalf("directory replaced: %v", err)
					}
				case "symlink":
					info, err := os.Lstat(path)
					if err != nil || info.Mode()&os.ModeSymlink == 0 {
						t.Fatalf("symlink replaced: %v", err)
					}
					assertOverwriteFile(t, filepath.Join(dir, "target.jpg"), data)
				case "oversized":
					info, err := os.Stat(path)
					if err != nil || info.Size() != maxInputSize+1 {
						t.Fatalf("oversized source changed: %v", err)
					}
				default:
					assertOverwriteFile(t, path, want)
				}
				assertNoOverwriteTemps(t, dir)
			})
		}
	}
}

func TestOverwriteBytesCapturesSuppliedSnapshot(t *testing.T) {
	data := jpegFixture(t, 100)
	for _, change := range []string{"replaced-before-compression", "absent-before-compression"} {
		t.Run(change, func(t *testing.T) {
			path := writeFixture(t, "snapshot.jpg", data)
			replacement := bytes.Clone(data)
			replacement[len(replacement)-1] ^= 1
			if change == "replaced-before-compression" {
				if err := os.WriteFile(path, replacement, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			result, err := Bytes(context.Background(), data, path, Options{Quality: 35})
			if err != nil {
				t.Fatal(err)
			}
			if err := result.Overwrite(); err == nil {
				t.Fatal("Bytes captured the current path instead of the supplied input")
			}
			if change == "replaced-before-compression" {
				assertOverwriteFile(t, path, replacement)
			} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing source was recreated: %v", err)
			}
			assertNoOverwriteTemps(t, filepath.Dir(path))
		})
	}
}

func TestOverwriteRejectsInvalidResult(t *testing.T) {
	data := jpegFixture(t, 100)
	for _, change := range []string{"nil", "fabricated", "changed-output", "empty-output", "truncated-output"} {
		t.Run(change, func(t *testing.T) {
			path := writeFixture(t, "source.jpg", data)
			result, err := File(context.Background(), path, Options{Quality: 35})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "nil":
				result = nil
			case "fabricated":
				result = &Result{SourcePath: path, sourcePath: path, Data: result.Data,
					OriginalSize: int64(len(data)), OutputSize: int64(len(result.Data))}
			case "changed-output":
				result.Data = bytes.Clone(result.Data)
				result.Data[len(result.Data)-1] ^= 1
			case "empty-output":
				result.Data = nil
			case "truncated-output":
				result.Data = result.Data[:len(result.Data)-1]
			}
			if err := result.Overwrite(); err == nil {
				t.Fatal("invalid result was allowed to overwrite")
			}
			assertOverwriteFile(t, path, data)
			assertNoOverwriteTemps(t, filepath.Dir(path))
		})
	}
}

func TestOverwriteRejectsUnchangedEvenIfPublicMetadataEdited(t *testing.T) {
	data := jpegFixture(t, 1)
	path := writeFixture(t, "source.jpg", data)
	result, err := File(context.Background(), path, Options{Quality: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unchanged {
		t.Fatal("fixture must have no saving")
	}
	if err := result.Overwrite(); err == nil {
		t.Fatal("unchanged result allowed to overwrite")
	}
	result.Unchanged = false
	result.OriginalSize = int64(len(data) + 1)
	result.OutputSize = 1
	if err := result.Overwrite(); err == nil {
		t.Fatal("public metadata edits bypassed the original size snapshot")
	}
	assertOverwriteFile(t, path, data)
	assertNoOverwriteTemps(t, filepath.Dir(path))
}

func TestOverwriteUsesCapturedAbsoluteSource(t *testing.T) {
	data := jpegFixture(t, 100)
	path := writeFixture(t, "source.jpg", data)
	result, err := File(context.Background(), path, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	other := writeFixture(t, "other.jpg", data)
	result.SourcePath = other
	if err := result.Overwrite(); err != nil {
		t.Fatal(err)
	}
	assertOverwriteFile(t, path, result.Data)
	assertOverwriteFile(t, other, data)
}

func TestOverwriteFinalCheckCleansStagedOutput(t *testing.T) {
	data := jpegFixture(t, 100)
	for _, change := range []string{"content", "identity", "deleted", "symlink"} {
		t.Run(change, func(t *testing.T) {
			path := writeFixture(t, "source.jpg", data)
			result, err := File(context.Background(), path, Options{Quality: 35})
			if err != nil {
				t.Fatal(err)
			}
			source, err := verifyOverwriteSource(path, result.snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := bytes.Clone(data)
			switch change {
			case "content":
				want[len(want)-1] ^= 1
				if err := os.WriteFile(path, want, 0600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				// Keep the old inode alive so the filesystem cannot reuse it.
				if err := os.Rename(path, path+".previous"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "deleted", "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					target := filepath.Join(filepath.Dir(path), "target.jpg")
					if err := os.WriteFile(target, data, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Exercise the actual write/Sync/close/recheck transaction with an
			// earlier observation, without introducing a timing-dependent race
			// or a production callback to mutate the source during Overwrite.
			if err := replaceOverwriteSource(path, result.Data, result.snapshot, source, nil); err == nil {
				t.Fatal("final source check accepted a changed identity/content")
			}
			if change == "deleted" {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deleted source recreated: %v", err)
				}
			} else {
				assertOverwriteFile(t, path, want)
			}
			if change == "symlink" {
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("final symlink was replaced: %v", err)
				}
			}
			assertNoOverwriteTemps(t, filepath.Dir(path))
		})
	}
}

func TestOverwritePNG(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native PNG bundle currently ships for darwin/arm64")
	}
	data := pngScreenshotFixture(t)
	path := writeFixture(t, "source.png", data)
	result, err := File(context.Background(), path, Options{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged {
		t.Fatal("PNG fixture must save bytes")
	}
	if err := result.Overwrite(); err != nil {
		t.Fatal(err)
	}
	assertOverwriteFile(t, path, result.Data)
	assertPNGResult(t, result, data, 320, 192)
	assertNoOverwriteTemps(t, filepath.Dir(path))
}

func TestOverwriteWithBackupMovesOriginalBeforeReplacing(t *testing.T) {
	data := jpegFixture(t, 100)
	path := writeFixture(t, "source.jpg", data)
	result, err := File(context.Background(), path, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged || len(result.Data) >= len(data) {
		t.Fatal("fixture must save bytes")
	}
	trashDir := t.TempDir()
	var trashed string
	backup := func(src string) error {
		trashed = filepath.Join(trashDir, filepath.Base(src))
		return os.Rename(src, trashed)
	}
	if err := result.OverwriteWithBackup(backup); err != nil {
		t.Fatal(err)
	}
	assertOverwriteFile(t, path, result.Data)
	assertOverwriteFile(t, trashed, data)
	assertNoOverwriteTemps(t, filepath.Dir(path))
}

func TestOverwriteWithBackupFailureAndNoOpPreserveOriginal(t *testing.T) {
	data := jpegFixture(t, 100)
	t.Run("error", func(t *testing.T) {
		path := writeFixture(t, "source.jpg", data)
		result, err := File(context.Background(), path, Options{Quality: 35})
		if err != nil {
			t.Fatal(err)
		}
		err = result.OverwriteWithBackup(func(string) error {
			return errors.New("trash unavailable")
		})
		if err == nil || !strings.Contains(err.Error(), "移入废纸篓失败") {
			t.Fatalf("expected trash failure error, got %v", err)
		}
		assertOverwriteFile(t, path, data)
		assertNoOverwriteTemps(t, filepath.Dir(path))
	})
	t.Run("noop", func(t *testing.T) {
		path := writeFixture(t, "source.jpg", data)
		result, err := File(context.Background(), path, Options{Quality: 35})
		if err != nil {
			t.Fatal(err)
		}
		err = result.OverwriteWithBackup(func(string) error {
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "未能移入废纸篓") {
			t.Fatalf("expected noop backup error, got %v", err)
		}
		assertOverwriteFile(t, path, data)
		assertNoOverwriteTemps(t, filepath.Dir(path))
	})
}
