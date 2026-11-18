package toolcache

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsurePrivateDirClearsSpecialPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix special permission bits")
	}
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700|os.ModeSetgid|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky); got != 0o700 {
		t.Fatalf("cache retains special permissions: %v", got)
	}
}

func TestVerifyRejectsSpecialExecutablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix special permission bits")
	}
	data := []byte("verified executable content")
	sum := sha256.Sum256(data)
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, special := range map[string]os.FileMode{
		"setuid": os.ModeSetuid,
		"setgid": os.ModeSetgid,
		"sticky": os.ModeSticky,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.Chmod(path, 0o700|special); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(path, int64(len(data)), sum); err == nil {
				t.Fatal("executable with special permissions accepted")
			}
			if _, err := Stage(filepath.Dir(path), path, data); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(path, int64(len(data)), sum); err != nil {
				t.Fatalf("staged ordinary executable rejected: %v", err)
			}
		})
	}
}
