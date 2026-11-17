package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanBatchPathsDeduplicatesDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "original", "album")
	alias := filepath.Join(root, "alias")
	writeTestJPEG(t, filepath.Join(directory, "one.jpg"), 8, 4, 80)
	writeTestJPEG(t, filepath.Join(directory, "two.jpg"), 12, 6, 80)
	if err := os.Symlink(filepath.Dir(directory), alias); err != nil {
		t.Fatal(err)
	}
	aliasedDirectory := filepath.Join(alias, "album")
	for _, scenario := range []struct {
		name   string
		inputs []string
	}{
		{"folders", []string{directory, aliasedDirectory}},
		{"files", []string{
			filepath.Join(directory, "one.jpg"), filepath.Join(aliasedDirectory, "one.jpg"),
			filepath.Join(aliasedDirectory, "two.jpg"), filepath.Join(directory, "two.jpg"),
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			scan, err := scanBatchPaths(context.Background(), scenario.inputs, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(scan.items) != 2 || scan.duplicates != 2 {
				t.Fatalf("directory aliases must share file keys: items=%d duplicates=%d", len(scan.items), scan.duplicates)
			}
			for index, name := range []string{"one.jpg", "two.jpg"} {
				item := scan.items[index]
				if item.status != batchPending || item.relPath != name ||
					item.absPath != canonicalBatchKey(filepath.Join(directory, name)) {
					t.Fatalf("alias scan changed item identity or relative path: %+v", item)
				}
			}
			existing := map[string]bool{scan.items[0].absPath: true, scan.items[1].absPath: true}
			appended, err := scanBatchPaths(context.Background(), []string{aliasedDirectory}, existing, len(existing))
			if err != nil {
				t.Fatal(err)
			}
			if len(appended.items) != 0 || appended.duplicates != 2 {
				t.Fatalf("append must recognize prior canonical keys: items=%d duplicates=%d", len(appended.items), appended.duplicates)
			}
		})
	}
}

func TestScanBatchPathsKeepsExplicitFileSymlinksDistinct(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photo.jpg")
	link := filepath.Join(root, "link.jpg")
	writeTestJPEG(t, source, 8, 4, 80)
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	scan, err := scanBatchPaths(context.Background(), []string{source, link}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.items) != 2 || scan.duplicates != 0 {
		t.Fatal("explicit file symlinks must retain their own leaf identity")
	}
	for _, item := range scan.items {
		if item.status != batchPending {
			t.Fatalf("explicit symlink input must remain readable: %+v", item)
		}
	}
	if scan.items[0].absPath == scan.items[1].absPath {
		t.Fatal("file leaf symlinks were resolved during parent normalization")
	}
	folderScan, err := scanBatchPaths(context.Background(), []string{root}, nil, 0)
	if err != nil || len(folderScan.items) != 1 || folderScan.items[0].path != source {
		t.Fatalf("folder traversal must still skip symlinks: %+v %v", folderScan, err)
	}
}

func TestScanBatchPathsRefreshesDirectoryAliasesBetweenScans(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	writeTestJPEG(t, filepath.Join(first, "photo.jpg"), 8, 4, 80)
	writeTestJPEG(t, filepath.Join(second, "photo.jpg"), 12, 6, 80)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	inputs := []string{filepath.Join(alias, "photo.jpg")}
	before, err := scanBatchPaths(context.Background(), inputs, nil, 0)
	if err != nil || len(before.items) != 1 {
		t.Fatalf("initial alias scan: %+v %v", before, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{before.items[0].absPath: true}
	after, err := scanBatchPaths(context.Background(), inputs, existing, 1)
	if err != nil || len(after.items) != 1 || after.duplicates != 0 {
		t.Fatalf("new scans must resolve changed aliases: %+v %v", after, err)
	}
	item := after.items[0]
	if item.absPath == before.items[0].absPath || item.absPath != canonicalBatchKey(filepath.Join(second, "photo.jpg")) ||
		item.width != 12 || item.height != 6 {
		t.Fatalf("alias cache leaked across scans: %+v", item)
	}
}

func TestCanonicalBatchKeyRetriesUnresolvedParents(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "alias")
	input := filepath.Join(alias, "photo.jpg")
	resolvedParents := make(map[string]batchParentPath)
	if got := canonicalBatchKeyWithParents(input, resolvedParents); got != filepath.Clean(input) {
		t.Fatalf("unresolved path fallback: got %q want %q", got, input)
	}
	directory := filepath.Join(root, "original")
	writeTestJPEG(t, filepath.Join(directory, "photo.jpg"), 8, 4, 80)
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	expected := canonicalBatchKey(filepath.Join(directory, "photo.jpg"))
	if got := canonicalBatchKeyWithParents(input, resolvedParents); got != expected {
		t.Fatalf("failed parent resolutions must be retried: got %q want %q", got, expected)
	}
}

func TestCanonicalBatchKeyRefreshesChangedParents(t *testing.T) {
	for _, scenario := range []string{"retarget", "rename", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			first := filepath.Join(root, "first")
			second := filepath.Join(root, "second")
			alias := filepath.Join(root, "alias")
			writeTestJPEG(t, filepath.Join(first, "photo.jpg"), 8, 4, 80)
			if err := os.Symlink(first, alias); err != nil {
				t.Fatal(err)
			}
			resolvedParents := make(map[string]batchParentPath)
			input := filepath.Join(alias, "photo.jpg")
			if got := canonicalBatchKeyWithParents(input, resolvedParents); got != canonicalBatchKey(filepath.Join(first, "photo.jpg")) {
				t.Fatalf("initial parent resolution: %q", got)
			}
			if scenario == "retarget" {
				writeTestJPEG(t, filepath.Join(second, "photo.jpg"), 12, 6, 80)
			} else if err := os.Rename(first, second); err != nil {
				t.Fatal(err)
			}
			if scenario == "redirect" {
				if err := os.Symlink(second, first); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(second, alias); err != nil {
					t.Fatal(err)
				}
			}
			expected := canonicalBatchKey(input)
			if got := canonicalBatchKeyWithParents(input, resolvedParents); got != expected {
				t.Fatalf("parent cache reused an outdated directory: got %q want %q", got, expected)
			}
		})
	}
}
