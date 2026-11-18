package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchOutputPreservesLegitimateDottedNames(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.jpg")
	for _, relative := range []string{"..photo.jpg", ".../photo.jpg", "album/..edit.jpg"} {
		path, err := resolveBatchOutputPath(root, source, relative, nil)
		if err != nil || path != filepath.Join(root, relative) {
			t.Fatalf("safe dotted path %q changed: %q, %v", relative, path, err)
		}
	}
	for _, relative := range []string{"../escape.jpg", "album/../../escape.jpg", "/escape.jpg"} {
		path, err := resolveBatchOutputPath(root, source, relative, nil)
		if err != nil || path != filepath.Join(root, "source.jpg") {
			t.Fatalf("unsafe path %q did not fall back: %q, %v", relative, path, err)
		}
	}
}

func TestBatchOutputRejectsNestedSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "album")); err != nil {
		t.Fatal(err)
	}
	planner := &batchOutputPlanner{}
	for _, relative := range []string{"album/photo.jpg", "album/new/photo.jpg"} {
		path, err := planner.resolve(root, filepath.Join(root, "source.jpg"), relative)
		if err == nil || path != "" || len(planner.used) != 0 {
			t.Fatalf("escaped output was reserved: path=%q, error=%v, used=%v", path, err, planner.used)
		}
	}
	option := presetSaveOptions{Mode: presetSaveFixed, UseSubfolder: true, Subfolder: "album"}
	if path, err := option.copyPath(filepath.Join(root, "source.jpg"), root, "photo.jpg", nil); err == nil || path != "" {
		t.Fatalf("preset subfolder escaped selected root: %q, %v", path, err)
	}
}

func TestBatchOutputAllowsRootAliasAndInternalAliases(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "album")
	if err := os.Mkdir(album, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(album, filepath.Join(root, "internal")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.jpg")
	for _, relative := range []string{"album/photo.jpg", "internal/new/photo.jpg", "missing/new/photo.jpg"} {
		path, err := resolveBatchOutputPath(alias, source, relative, nil)
		if err != nil || path != filepath.Join(alias, relative) {
			t.Fatalf("valid root/internal alias rejected: %q, %v", path, err)
		}
	}
}

func TestBatchOutputRechecksDirectoryAliasAndRejectsDanglingAlias(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	album := filepath.Join(root, "album")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(album, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(album, alias); err != nil {
		t.Fatal(err)
	}
	planner := &batchOutputPlanner{}
	source := filepath.Join(root, "source.jpg")
	if _, err := planner.resolve(root, source, "alias/photo.jpg"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{outside, filepath.Join(outside, "not-created")} {
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}
		before := len(planner.used)
		if path, err := planner.resolve(root, source, "alias/new/photo.jpg"); err == nil || path != "" || len(planner.used) != before {
			t.Fatalf("retargeted/dangling alias accepted: %q, %v", path, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateOutputDirectory(ctx, root, album); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
