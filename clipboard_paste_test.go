package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestPasteImagesCreatesQueue(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp", "files"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			var data transfer.Data
			var original []byte
			switch format {
			case "png":
				original = writeTestPNG(t, filepath.Join(root, "source.png"), 32, 24, true)
				data = transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, original)))
			case "jpeg":
				original = writeTestJPEG(t, filepath.Join(root, "source.jpg"), 40, 30, 95)
				data = transfer.New(transfer.NewItem(transfer.Bytes("image/jpeg", original)))
			case "webp":
				original = webPAppFixture(t, "alpha.webp")
				data = transfer.New(transfer.NewItem(transfer.Bytes("image/webp", original)))
			case "files":
				first, second := filepath.Join(root, "first.png"), filepath.Join(root, "second.jpg")
				writeTestPNG(t, first, 32, 24, false)
				writeTestJPEG(t, second, 40, 30, 95)
				var err error
				data, err = transfer.FileData(first, second)
				if err != nil {
					t.Fatal(err)
				}
			}
			app := newApplication()
			app.downloadDir = filepath.Join(root, "clipboard")
			updates := make(chan func(), 16)
			app.dispatchUpdate = func(update func()) { updates <- update }
			reads := 0
			app.readClipboardImages = func() (transfer.Data, error) {
				reads++
				return data, nil
			}
			app.pasteImages()
			waitForDropImports(t, app, updates)
			wantCount := 1
			if format == "files" {
				wantCount = 2
			}
			if reads != 1 || app.failed || app.clipboardImporting || app.batch == nil || len(app.batch.items) != wantCount {
				t.Fatalf("paste must import into a queue: reads=%d message=%q batch=%+v", reads, app.message, app.batch)
			}
			if format != "files" {
				imported, err := os.ReadFile(app.batch.items[0].path)
				if err != nil || !bytes.Equal(imported, original) {
					t.Fatalf("pasting must preserve the original bytes: %v", err)
				}
				if filepath.Dir(app.batch.items[0].path) != app.downloadDir {
					t.Fatal("clipboard image must be saved in the import directory")
				}
			}
			assertClipboardToast(t, app, "剪贴板图片已加入队列")
		})
	}
}

func TestPasteImagesAppendsAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first.jpg"), filepath.Join(root, "second.png")
	original := writeTestJPEG(t, first, 40, 30, 95)
	writeTestPNG(t, second, 32, 24, false)
	app := newApplication()
	selected, err := readSelection(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	app.completeSelection(context.Background(), selected, nil)
	app.downloadDir = filepath.Join(root, "clipboard")
	updates := make(chan func(), 16)
	app.dispatchUpdate = func(update func()) { updates <- update }
	data, err := transfer.FileData(second)
	if err != nil {
		t.Fatal(err)
	}
	app.readClipboardImages = func() (transfer.Data, error) { return data, nil }
	app.pasteImages()
	waitForDropImports(t, app, updates)
	if app.batch == nil || len(app.batch.items) != 2 || app.batch.items[0].path != first || app.batch.items[1].path != second {
		t.Fatal("paste must preserve a single selection when creating the queue")
	}
	app.batch.items[0].status = batchDone
	app.batch.items[0].savedPath = filepath.Join(root, "saved.jpg")
	before := app.batch.items[0]
	added := writeTestPNG(t, filepath.Join(root, "third.png"), 24, 16, true)
	data = transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, added)))
	app.pasteImages()
	waitForDropImports(t, app, updates)
	if app.failed || len(app.batch.items) != 3 || app.batch.items[0] != before {
		t.Fatal("pasting another image must append without resetting completed results")
	}
	data, err = transfer.FileData(second)
	if err != nil {
		t.Fatal(err)
	}
	app.pasteImages()
	waitForDropImports(t, app, updates)
	if app.failed || len(app.batch.items) != 3 || app.batch.items[0] != before {
		t.Fatal("duplicate file paste must preserve existing queue results")
	}
	current, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("paste must not modify the original file")
	}
	assertClipboardToast(t, app, "图片已在队列中")
}

func TestPasteImagesRejectsUnavailableOrBrokenClipboard(t *testing.T) {
	for _, scenario := range []string{"empty", "unsupported", "read failure", "broken image", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			app := testSelectedApplication()
			source := app.file
			app.downloadDir = t.TempDir()
			updates := make(chan func(), 16)
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.readClipboardImages = func() (transfer.Data, error) {
				switch scenario {
				case "unsupported":
					return transfer.Data{}, transfer.ErrFormat
				case "read failure":
					return transfer.Data{}, errors.New("clipboard unavailable")
				case "broken image", "cancel":
					return transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("broken")))), nil
				}
				return transfer.Data{}, nil
			}
			app.pasteImages()
			if scenario == "cancel" {
				app.cancelWork()
			}
			waitForDropImports(t, app, updates)
			if app.file != source || app.batch != nil || app.busy || app.clipboardImporting {
				t.Fatal("unsuccessful paste must preserve the current selection")
			}
			if scenario == "cancel" {
				if app.failed {
					t.Fatal("cancelled paste must remain neutral")
				}
				assertClipboardToast(t, app, "已取消粘贴图片")
			} else {
				if !app.failed || app.clipboardToast == nil || app.clipboardToast.Type != "error" {
					t.Fatalf("unsuccessful paste must notify the user: %q", app.message)
				}
			}
			files, err := os.ReadDir(app.downloadDir)
			if err != nil || len(files) != 0 {
				t.Fatal("rejected clipboard data must not leave imported files")
			}
		})
	}
}

func TestPasteImagesGuards(t *testing.T) {
	for _, guard := range []string{"busy", "settings", "overwrite", "batch overwrite", "closed", "pending drops"} {
		t.Run(guard, func(t *testing.T) {
			app := newApplication()
			switch guard {
			case "busy":
				app.busy = true
			case "settings":
				app.settingsOpen = true
			case "overwrite":
				app.confirmOverwrite = true
			case "batch overwrite":
				app.confirmBatchOverwrite = true
			case "closed":
				app.closed = true
			case "pending drops":
				app.dropQueue = []transfer.Data{transfer.TextData("unused")}
			}
			app.readClipboardImages = func() (transfer.Data, error) {
				t.Fatal("guarded paste must not read the clipboard")
				return transfer.Data{}, nil
			}
			app.pasteImages()
			if app.canPasteImages() || app.clipboardImporting {
				t.Fatal("guarded paste must not start importing")
			}
		})
	}
}

func TestPasteImagesShortcutAndButton(t *testing.T) {
	for _, width := range []int{760, 980} {
		app := newApplication()
		reads := 0
		app.readClipboardImages = func() (transfer.Data, error) {
			reads++
			return transfer.Data{}, nil
		}
		tester := ui.NewTester(app.view, width, 660)
		button, ok := tester.AccessByRole(ui.RoleButton, "粘贴图片")
		if !ok || button.Disabled || button.Offscreen || button.Bounds.X < 0 || button.Bounds.X+button.Bounds.W > float32(width) {
			t.Fatalf("paste button must fit %d-point window: %+v", width, button)
		}
		tester.Key(ui.Cmd, ui.KeyV)
		if reads != 1 || !strings.Contains(app.message, "剪贴板中没有") {
			t.Fatal("paste shortcut must invoke clipboard import")
		}
		app.busy = true
		tester.Frame()
		tester.Key(ui.Cmd, ui.KeyV)
		button, ok = tester.AccessByRole(ui.RoleButton, "粘贴图片")
		if reads != 1 || !ok || !button.Disabled {
			t.Fatal("paste action must be disabled while busy")
		}
	}
}

func TestPasteImagesLeavesTextFieldPasteAlone(t *testing.T) {
	app := testSelectedApplication()
	app.batch = newBatchState([]batchItem{{path: app.file.path, status: batchPending}})
	app.readClipboardImages = func() (transfer.Data, error) {
		t.Fatal("text-field paste must not import images")
		return transfer.Data{}, nil
	}
	tester := ui.NewTester(app.view, 980, 660)
	search, ok := tester.AccessByRole(ui.RoleTextField, "筛选文件名")
	if !ok {
		t.Fatal("missing queue search field")
	}
	tester.ClickAt(search.Bounds.X+10, search.Bounds.Y+10)
	tester.SetClipboard("sample")
	tester.Key(ui.Cmd, ui.KeyV)
	if app.batch.query != "sample" || app.clipboardImporting {
		t.Fatal("focused search field must receive pasted text")
	}
}
