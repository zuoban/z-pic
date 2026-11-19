package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/dropimport"
)

func newLoopbackDropTransport(t *testing.T) *http.Transport {
	t.Helper()
	transport := dropimport.NewTransport()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func waitForDropImports(t *testing.T, app *application, updates <-chan func()) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for app.busy || len(app.dropQueue) > 0 {
		select {
		case update := <-updates:
			update()
		case <-timeout.C:
			t.Fatalf("drop import did not finish: phase=%q pending=%d", app.phase, len(app.dropQueue))
		}
	}
	app.saves.Wait()
}

func TestFloatingLocalDropsWaitForGuardAndPreserveSingleSource(t *testing.T) {
	for _, guard := range []string{"busy", "settings", "overwrite", "batch-overwrite"} {
		t.Run(guard, func(t *testing.T) {
			root := t.TempDir()
			selectedPath := filepath.Join(root, "selected.jpg")
			droppedPath := filepath.Join(root, "dropped.jpg")
			selectedBytes := writeTestJPEG(t, selectedPath, 80, 60, 95)
			droppedBytes := writeTestJPEG(t, droppedPath, 120, 90, 95)
			selected, err := readSelection(context.Background(), selectedPath)
			if err != nil {
				t.Fatal(err)
			}
			updates := make(chan func(), 16)
			app := newApplication()
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.downloadDir = filepath.Join(root, "downloads")
			app.completeSelection(context.Background(), selected, nil)
			app.busy = guard == "busy"
			app.settingsOpen = guard == "settings"
			app.confirmOverwrite = guard == "overwrite"
			app.confirmBatchOverwrite = guard == "batch-overwrite"
			data, err := transfer.FileData(droppedPath)
			if err != nil {
				t.Fatal(err)
			}
			app.receiveDrop(data)
			if len(app.dropQueue) != 1 || app.dropImporting || app.file != selected || app.batch != nil {
				t.Fatal("guarded drop must remain pending and preserve the selected image")
			}
			app.busy, app.settingsOpen, app.confirmOverwrite, app.confirmBatchOverwrite = false, false, false, false
			app.startNextDropImport()
			waitForDropImports(t, app, updates)
			if app.batch == nil || len(app.batch.items) != 2 || app.failed ||
				app.batch.items[0].path != selectedPath || app.batch.items[1].path != droppedPath {
				t.Fatalf("drop must create a queue containing both originals: message=%q batch=%+v", app.message, app.batch)
			}
			for path, original := range map[string][]byte{selectedPath: selectedBytes, droppedPath: droppedBytes} {
				current, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(current, original) {
					t.Fatalf("import modified a source: %q: %v", path, err)
				}
			}
		})
	}
}

func TestFloatingDropsAppendAndDeduplicateWithoutResettingResults(t *testing.T) {
	root := t.TempDir()
	donePath := filepath.Join(root, "done.jpg")
	pendingPath := filepath.Join(root, "pending.jpg")
	addedPath := filepath.Join(root, "added.png")
	savedPath := filepath.Join(root, "out", "done.jpg")
	writeTestJPEG(t, donePath, 80, 60, 100)
	writeTestJPEG(t, pendingPath, 80, 60, 100)
	writeTestPNG(t, addedPath, 32, 24, false)
	saved := writeTestJPEG(t, savedPath, 80, 60, 60)
	scan, err := scanBatchPaths(context.Background(), []string{donePath, pendingPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 16)
	app := newApplication()
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(root, "downloads")
	app.batch = newBatchState(scan.items)
	app.batch.items[0].status = batchDone
	app.batch.items[0].savedPath = savedPath
	app.batch.items[0].outputSize = int64(len(saved))
	app.batch.filter, app.batch.query = batchFilterDone, "done"
	before := app.batch.items[0]
	data, err := transfer.FileData(addedPath)
	if err != nil {
		t.Fatal(err)
	}
	app.receiveDrop(data)
	app.receiveDrop(data)
	waitForDropImports(t, app, updates)
	if len(app.batch.items) != 3 || app.batch.items[0] != before || app.batch.items[2].path != addedPath {
		t.Fatalf("drops must append once and preserve completed results: %+v", app.batch.items)
	}
	if app.batch.filter != batchFilterAll || app.batch.query != "" {
		t.Fatal("new items must be visible after an existing filter")
	}
	current, err := os.ReadFile(savedPath)
	if err != nil || !bytes.Equal(current, saved) {
		t.Fatalf("drop modified an earlier output: %v", err)
	}
	if !strings.Contains(app.dropMessage, "已在批量队列") {
		t.Fatalf("repeated drop must report deduplication: %q", app.dropMessage)
	}
}

func TestFloatingWebImagesDownloadAndReuseOriginals(t *testing.T) {
	root := t.TempDir()
	original := writeTestJPEG(t, filepath.Join(root, "fixture.jpg"), 80, 60, 100)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/bad" {
			response.Header().Set("Content-Type", "text/html")
			fmt.Fprint(response, "<html>sign in</html>")
			return
		}
		response.Header().Set("Content-Type", "image/jpeg")
		_, _ = response.Write(original)
	}))
	defer server.Close()
	updates := make(chan func(), 32)
	app := newApplication()
	app.dropTransport = newLoopbackDropTransport(t)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(root, "downloads")
	data := transfer.New(transfer.NewItem(
		transfer.Bytes(transfer.HTML, []byte(`<a href="/page"><img src="/image"></a>`)),
		transfer.Bytes(transfer.URIList, []byte(server.URL+"/page")),
	))
	app.receiveDrop(data)
	waitForDropImports(t, app, updates)
	if app.batch == nil || len(app.batch.items) != 1 || app.dropFailed {
		t.Fatalf("browser image must download and create a one-item queue: %q", app.dropMessage)
	}
	downloadedPath := app.batch.items[0].path
	if filepath.Dir(downloadedPath) != app.downloadDir || filepath.Ext(downloadedPath) != ".jpg" {
		t.Fatalf("download must use its real format inside the download directory: %q", downloadedPath)
	}
	current, err := os.ReadFile(downloadedPath)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("download must retain the original image bytes: %v", err)
	}
	repeated, err := transfer.URLData(server.URL + "/image")
	if err != nil {
		t.Fatal(err)
	}
	app.receiveDrop(repeated)
	waitForDropImports(t, app, updates)
	if len(app.batch.items) != 1 || requests.Load() != 1 {
		t.Fatalf("repeated URL must reuse its download and queue entry: count=%d requests=%d", len(app.batch.items), requests.Load())
	}
	mixed := transfer.New(transfer.NewItem(
		transfer.Bytes(transfer.URIList, []byte(server.URL+"/bad\r\n"+server.URL+"/second-image")),
	))
	app.receiveDrop(mixed)
	waitForDropImports(t, app, updates)
	if len(app.batch.items) != 2 || !app.dropFailed || !app.failed || !strings.Contains(app.dropMessage, "JPEG") {
		t.Fatalf("valid images must survive a mixed download failure: %q batch=%+v", app.dropMessage, app.batch)
	}
}

func TestFloatingSameNameImagesKeepImportOrder(test *testing.T) {
	root := test.TempDir()
	original := writeTestJPEG(test, filepath.Join(root, "fixture.jpg"), 80, 60, 100)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(original)
	}))
	defer server.Close()
	updates := make(chan func(), 32)
	app := newApplication()
	app.dropTransport = newLoopbackDropTransport(test)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(root, "downloads")
	var urls []string
	for _, path := range []string{"/first/photo.jpg", "/second/photo.jpg", "/third/photo-1.jpg", "/fourth/photo.jpg"} {
		urls = append(urls, server.URL+path)
	}
	app.receiveDrop(transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte(strings.Join(urls, "\r\n"))))))
	waitForDropImports(test, app, updates)
	if app.batch == nil || len(app.batch.items) != len(urls) || app.dropFailed {
		test.Fatalf("same-name import failed: %q, %+v", app.dropMessage, app.batch)
	}
	for index, name := range []string{"photo.jpg", "photo-1.jpg", "photo-1-1.jpg", "photo-2.jpg"} {
		path := app.batch.items[index].path
		if path != filepath.Join(app.downloadDir, name) || app.downloadedURLs[urls[index]] != path {
			test.Fatalf("item %d lost its name or URL mapping: %q, %v", index, path, app.downloadedURLs)
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, original) {
			test.Fatalf("download changed source bytes: %q, %v", path, err)
		}
	}
}

func TestFloatingDownloadCanCancel(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	updates := make(chan func(), 16)
	app := newApplication()
	app.dropTransport = newLoopbackDropTransport(t)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(t.TempDir(), "downloads")
	data, err := transfer.URLData(server.URL + "/image")
	if err != nil {
		t.Fatal(err)
	}
	app.receiveDrop(data)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	tester := ui.NewTester(app.floatingView, 280, 256)
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	waitForDropImports(t, app, updates)
	if app.batch != nil || !app.dropFailed || !strings.Contains(app.dropMessage, "取消") {
		t.Fatalf("cancel must leave the queue empty and report cancellation: %q", app.dropMessage)
	}
	entries, err := os.ReadDir(app.downloadDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("canceled download leaked a partial file")
	}
}

func TestFloatingFailuresRemainVisibleAcrossLaterSuccessfulDrops(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "local.jpg")
	writeTestJPEG(t, localPath, 80, 60, 100)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "not found", http.StatusNotFound)
	}))
	defer server.Close()
	updates := make(chan func(), 16)
	app := newApplication()
	app.dropTransport = newLoopbackDropTransport(t)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(root, "downloads")
	app.busy = true
	failed, err := transfer.URLData(server.URL + "/image")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := transfer.FileData(localPath)
	if err != nil {
		t.Fatal(err)
	}
	app.receiveDrop(failed)
	app.receiveDrop(valid)
	app.busy = false
	app.startNextDropImport()
	waitForDropImports(t, app, updates)
	message, isError := app.floatingStatus()
	if app.batch == nil || len(app.batch.items) != 1 || len(app.dropFailures) != 1 ||
		!isError || !strings.Contains(message, "HTTP 404") {
		t.Fatalf("later success must retain earlier failure feedback: %q errors=%v", message, app.dropFailures)
	}
	tester := ui.NewTester(app.floatingView, 280, 256)
	if err := tester.Click("清除提示"); err != nil {
		t.Fatal(err)
	}
	if _, isError := app.floatingStatus(); isError || len(app.dropFailures) != 0 || len(app.batch.items) != 1 {
		t.Fatal("acknowledging failures must clear feedback and retain imported images")
	}
}

func TestFloatingDownloadFolderFailureStaysVisibleWhileDropsWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-folder")
	original := []byte("keep this file")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	app.downloadDir = path
	app.busy = true
	app.receiveDrop(transfer.TextData("https://example.com/image.png"))
	app.openDownloadedImagesDir()
	message, failed := app.floatingStatus()
	if !failed || !strings.Contains(message, "无法打开下载位置") {
		t.Fatalf("download-folder errors must remain visible while drops wait: %q", message)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("opening an invalid download directory changed an existing file: %v", err)
	}
}

func TestFloatingControlsAndStatusFitBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, expanded := range []bool{false, true} {
			for _, state := range []string{"idle", "busy", "pending", "error", "error-history"} {
				t.Run(fmt.Sprintf("dark=%v/expanded=%v/%s", dark, expanded, state), func(t *testing.T) {
					app := newApplication()
					app.floating.expanded = expanded
					if state == "busy" {
						app.busy, app.dropImporting, app.phase = true, true, "正在下载 1 / 3 张图片"
					}
					if state == "pending" {
						app.dropQueue = []transfer.Data{transfer.TextData("https://example.com/image.png")}
					}
					if state == "error" {
						app.dropMessage, app.dropFailed = "下载失败，请检查图片链接或网络连接后重试。", true
					}
					if state == "error-history" {
						app.rememberDropFailure("图片下载失败：HTTP 404，请检查图片地址。")
					}
					width, height := floatingCompactWidth, floatingCompactHeight
					labels := []string{"收起", "展开图片", "添加图片"}
					if expanded {
						width, height = floatingExpandedWidth, floatingExpandedHeight
						labels = []string{"收起图片", "网格视图", "列表视图", "查看队列", "下载位置"}
					}
					tester := ui.NewTester(func(context *ui.Context) {
						theme := ui.LightTheme()
						if dark {
							theme = ui.DarkTheme()
						}
						context.SetTheme(theme)
						app.floatingView(context)
					}, width, height)
					for _, label := range labels {
						button, ok := tester.AccessByRole(ui.RoleButton, label)
						if !ok || button.Offscreen || button.Bounds.H < 28 || button.Bounds.X < 0 ||
							button.Bounds.Y < 0 || button.Bounds.X+button.Bounds.W > float32(width) ||
							button.Bounds.Y+button.Bounds.H > float32(height) {
							t.Fatalf("floating control must fit: %s %+v", label, button)
						}
					}
					if state == "error" {
						if alert, ok := tester.AccessByRole(ui.RoleAlert, app.dropMessage); !ok || alert.Offscreen {
							t.Fatal("floating errors must be visible and accessible")
						}
					}
					if state == "error-history" {
						clear, ok := tester.AccessByRole(ui.RoleButton, "清除提示")
						if !ok || clear.Offscreen || clear.Bounds.X+clear.Bounds.W > float32(width) {
							t.Fatalf("error acknowledgement must fit: %+v", clear)
						}
					}
				})
			}
		}
	}
	app := newApplication()
	main := ui.NewTester(app.view, 880, 660)
	if err := main.Click("悬浮窗"); err != nil || !app.floatingOpen {
		t.Fatalf("main header must open the floating window: %v", err)
	}
	if err := main.Click("收起悬浮窗"); err != nil || app.floatingOpen {
		t.Fatalf("main header must close the floating window: %v", err)
	}
}

func TestFloatingDropLimitsAndClosedApplication(t *testing.T) {
	app := newApplication()
	app.busy = true
	for range maxPendingDrops + 1 {
		app.receiveDrop(transfer.TextData("https://example.com/image.png"))
	}
	if len(app.dropQueue) != maxPendingDrops || !app.dropFailed || !strings.Contains(app.dropMessage, "稍后") {
		t.Fatal("pending drops must be bounded and report overflow")
	}
	if message, failed := app.floatingStatus(); !failed || !strings.Contains(message, "稍后") {
		t.Fatal("queue overflow must be visible while guarded imports remain pending")
	}
	app.closed = true
	app.receiveDrop(transfer.TextData("https://example.com/other.png"))
	app.setFloatingOpen(true)
	if len(app.dropQueue) != maxPendingDrops || app.floatingOpen {
		t.Fatal("closed application must ignore new drops and window requests")
	}
	updates := make(chan func(), 1)
	app.dispatchUpdate = func(update func()) { updates <- update }
	called := false
	app.update(func() { called = true })
	(<-updates)()
	if called {
		t.Fatal("a late background result must not mutate a closed application")
	}
}

func TestFloatingDownloadedURLCacheIsBounded(t *testing.T) {
	app := newApplication()
	for i := range maxCachedDownloadURLs + 50 {
		url := fmt.Sprintf("https://cdn.example.com/%d.png", i)
		app.recordDownloadedURLs(map[string]string{url: fmt.Sprintf("/tmp/%d.png", i)})
	}
	if len(app.downloadedURLs) != maxCachedDownloadURLs || len(app.downloadedURLOrder) != maxCachedDownloadURLs {
		t.Fatalf("downloadedURLs not bounded: map=%d order=%d", len(app.downloadedURLs), len(app.downloadedURLOrder))
	}
	if _, exists := app.downloadedURLs["https://cdn.example.com/0.png"]; exists {
		t.Fatal("oldest URL was not evicted")
	}
	lastURL := fmt.Sprintf("https://cdn.example.com/%d.png", maxCachedDownloadURLs+49)
	if app.downloadedURLs[lastURL] == "" {
		t.Fatal("newest URL was not retained")
	}
}
