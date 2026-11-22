package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b    string
		want    int
		wantErr bool
	}{
		{"0.1.0", "0.1.0", 0, false},
		{"v0.2.0", "0.1.0", 1, false},
		{"0.1.0", "v0.2.0", -1, false},
		{"1.0.0", "0.99.99", 1, false},
		{"0.1.1", "0.1.0", 1, false},
		{"1.0.0", "1.0.0-rc.1", 1, false},
		{"1.0.0-rc.2", "1.0.0-rc.1", 1, false},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1, false},
		{"1.0.0+build.1", "1.0.0+build.2", 0, false},
		{"invalid", "0.1.0", 0, true},
		{"1.2", "0.1.0", 0, true},
		{"1.2.-3", "0.1.0", 0, true},
	}
	for _, tc := range tests {
		got, err := compareSemver(tc.a, tc.b)
		if (err != nil) != tc.wantErr {
			t.Errorf("compareSemver(%q, %q) err=%v wantErr=%v", tc.a, tc.b, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSanitizeReleaseURL(t *testing.T) {
	valid := "https://github.com/zuoban/z-pic/releases/tag/v0.2.0"
	if got := sanitizeReleaseURL(valid); got != valid {
		t.Fatalf("valid release URL changed: %q", got)
	}
	for _, bad := range []string{
		"",
		"http://github.com/zuoban/z-pic/releases/tag/v0.2.0",
		"javascript:alert(1)",
		"https://evil.example.com/zuoban/z-pic/releases/tag/v0.2.0",
		"https://user:pass@github.com/zuoban/z-pic/releases/tag/v0.2.0",
		"https://github.com/other/repo/releases/tag/v0.2.0",
		"https://github.com/zuoban/z-pic/releases/../issues",
		"https://github.com/zuoban/z-pic/releases/%2e%2e/issues",
	} {
		if got := sanitizeReleaseURL(bad); got != defaultReleasePageURL {
			t.Errorf("sanitizeReleaseURL(%q) = %q, want fallback %q", bad, got, defaultReleasePageURL)
		}
	}
}

func TestCheckForUpdatesHTTPScenarios(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("User-Agent"), "z-pic/") {
				t.Errorf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
			}
			_ = json.NewEncoder(w).Encode(githubReleasePayload{
				TagName: "v0.2.0",
				Name:    "Z Pic v0.2.0",
				HTMLURL: "https://github.com/zuoban/z-pic/releases/tag/v0.2.0",
			})
		}))
		defer srv.Close()

		res, err := checkForUpdates(context.Background(), updateCheckerConfig{
			endpoint:       srv.URL,
			currentVersion: "0.1.0",
			client:         srv.Client(),
		})
		if err != nil {
			t.Fatalf("checkForUpdates failed: %v", err)
		}
		if !res.HasUpdate || res.LatestVersion != "0.2.0" || res.ReleaseURL != "https://github.com/zuoban/z-pic/releases/tag/v0.2.0" {
			t.Fatalf("unexpected update result: %+v", res)
		}
	})

	t.Run("already up to date", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(githubReleasePayload{
				TagName: "v0.1.0",
				HTMLURL: "https://github.com/zuoban/z-pic/releases/tag/v0.1.0",
			})
		}))
		defer srv.Close()

		res, err := checkForUpdates(context.Background(), updateCheckerConfig{
			endpoint:       srv.URL,
			currentVersion: "0.1.0",
			client:         srv.Client(),
		})
		if err != nil || res.HasUpdate || res.NoReleaseYet {
			t.Fatalf("expected up-to-date result, got res=%+v err=%v", res, err)
		}
	})

	t.Run("404 means no release published yet", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		res, err := checkForUpdates(context.Background(), updateCheckerConfig{
			endpoint:       srv.URL,
			currentVersion: "0.1.0",
			client:         srv.Client(),
		})
		if err != nil || res.HasUpdate || !res.NoReleaseYet {
			t.Fatalf("expected NoReleaseYet=true without error, got res=%+v err=%v", res, err)
		}
	})

	t.Run("rate limit returns friendly error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		_, err := checkForUpdates(context.Background(), updateCheckerConfig{
			endpoint:       srv.URL,
			currentVersion: "0.1.0",
			client:         srv.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "频繁") {
			t.Fatalf("expected rate-limit error, got %v", err)
		}
	})
}

func TestAppUpdateCheckFlowAndTrayItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubReleasePayload{
			TagName: "v0.3.0",
			Name:    "Z Pic v0.3.0",
			HTMLURL: "https://github.com/zuoban/z-pic/releases/tag/v0.3.0",
		})
	}))
	defer srv.Close()

	app := testSelectedApplication()
	app.updateChecker = updateCheckerConfig{
		endpoint:       srv.URL,
		currentVersion: "0.1.0",
		client:         srv.Client(),
	}
	updates := make(chan func(), 4)
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	var openedURL string
	app.openExternalURL = func(u string) error { openedURL = u; return nil }

	// Verify tray menu exposes Check for Updates item.
	menu := app.buildTrayMenu()
	if menu.ItemByID("tray-check-updates") == nil {
		t.Fatal("tray menu must include tray-check-updates item")
	}

	app.setSettingsOpen(true)
	app.checkAppUpdates()
	if app.updateState != updateChecking {
		t.Fatalf("expected updateChecking state, got %v", app.updateState)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for update check callback")
	}
	if app.updateState != updateAvailable || app.updateResult.LatestVersion != "0.3.0" {
		t.Fatalf("expected updateAvailable v0.3.0, got state=%v res=%+v msg=%q", app.updateState, app.updateResult, app.updateMessage)
	}

	tester := ui.NewTester(app.settingsView, 724, 960)
	if err := tester.Click("关于"); err != nil {
		t.Fatalf("switching to about tab failed: %v", err)
	}
	if !tester.HasText("关于与更新") || !tester.HasText("新版本 v0.3.0") || !tester.HasText("前往下载") {
		t.Fatal("about settings view must show version badge and download button when update is available")
	}
	app.openReleaseDownloadPage()
	if openedURL != "https://github.com/zuoban/z-pic/releases/tag/v0.3.0" {
		t.Fatalf("openedURL = %q, want release tag URL", openedURL)
	}

	// Failing to launch external browser marks updateMessageFailed while keeping download available.
	app.openExternalURL = func(string) error { return errors.New("browser unavailable") }
	app.openReleaseDownloadPage()
	if app.updateState != updateAvailable || !app.updateMessageFailed || !strings.Contains(app.updateMessage, "无法打开下载页面") {
		t.Fatalf("expected updateMessageFailed=true with updateAvailable retained, got state=%v failed=%v msg=%q", app.updateState, app.updateMessageFailed, app.updateMessage)
	}
	// Retrying successfully restores the normal update-available message.
	app.openExternalURL = func(u string) error { openedURL = u; return nil }
	app.openReleaseDownloadPage()
	if app.updateMessageFailed || !strings.Contains(app.updateMessage, "发现新版本 v0.3.0") {
		t.Fatalf("expected retry success to clear updateMessageFailed, got failed=%v msg=%q", app.updateMessageFailed, app.updateMessage)
	}
}

func TestOpenAtLoginSettingsAndSilentStartup(t *testing.T) {
	if !shouldStartMainWindowHidden(true, true, 0) {
		t.Fatal("opened at login with keepInMenuBar and no files should start hidden")
	}
	if shouldStartMainWindowHidden(false, true, 0) || shouldStartMainWindowHidden(true, false, 0) || shouldStartMainWindowHidden(true, true, 1) {
		t.Fatal("shouldStartMainWindowHidden returned true unexpectedly")
	}

	app := testSelectedApplication()
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	loginEnabled := false
	setCalls := 0
	app.openAtLoginSupported = func() bool { return true }
	app.openAtLoginState = func() bool { return loginEnabled }
	app.setOpenAtLogin = func(v bool) error {
		setCalls++
		loginEnabled = v
		return nil
	}

	app.setSettingsOpen(true)
	if app.settingsHaveChanges() {
		t.Fatal("clean settings should not report changes")
	}
	app.editor.openAtLogin = true
	if !app.settingsHaveChanges() {
		t.Fatal("toggling openAtLogin must mark settings dirty")
	}
	app.saveSettingsEdits()
	if setCalls != 1 || !loginEnabled || app.settingsOpen {
		t.Fatalf("expected openAtLogin applied and settings closed: calls=%d enabled=%v open=%v", setCalls, loginEnabled, app.settingsOpen)
	}

	// Saving again without changing openAtLogin must not call OS API redundantly.
	app.setSettingsOpen(true)
	app.saveSettingsEdits()
	if setCalls != 1 {
		t.Fatalf("unchanged openAtLogin should not invoke setOpenAtLogin again: calls=%d", setCalls)
	}

	// Error from OS API stays in settings with clear message while preserving saved file settings and clearing stale notice.
	app.setOpenAtLogin = func(bool) error { return errors.New("permission denied") }
	app.setSettingsOpen(true)
	app.editor.notice = "旧提示"
	app.editor.openAtLogin = false
	app.editor.keepInMenuBar = true
	app.saveSettingsEdits()
	if !app.settingsOpen || !strings.Contains(app.editor.message, "更新开机启动失败") || app.editor.notice != "" {
		t.Fatalf("expected error kept visible and notice cleared on setOpenAtLogin failure: open=%v msg=%q notice=%q", app.settingsOpen, app.editor.message, app.editor.notice)
	}
	if !app.keepInMenuBar {
		t.Fatal("in-memory settings must remain aligned with already persisted JSON on login-item error")
	}
}

func TestWindowProgressBarSyncLifecycle(t *testing.T) {
	app := newApplication()
	var history []mygo.ProgressBar
	app.setWindowProgress = func(bar mygo.ProgressBar) {
		history = append(history, bar)
	}

	// Idle produces no redundant update.
	app.syncWindowProgressIfChanged()
	if len(history) != 0 {
		t.Fatalf("idle sync should not emit progress bar updates, got %+v", history)
	}

	// Batch started (0/4) -> indeterminate.
	app.batch = newBatchState([]batchItem{{path: "a.png"}, {path: "b.png"}, {path: "c.png"}, {path: "d.png"}})
	app.batch.running = true
	app.batch.total = 4
	app.batch.done = 0
	app.busy = true
	app.syncWindowProgressIfChanged()
	if len(history) != 1 || history[0].State != mygo.ProgressIndeterminate {
		t.Fatalf("expected indeterminate on batch start, got %+v", history)
	}

	// Progress 2/4 -> normal 0.5.
	app.batch.done = 2
	app.syncWindowProgressIfChanged()
	if len(history) != 2 || history[1].State != mygo.ProgressNormal || history[1].Value != 0.5 {
		t.Fatalf("expected 0.5 normal progress, got %+v", history)
	}

	// Identical sync is deduplicated.
	app.syncWindowProgressIfChanged()
	if len(history) != 2 {
		t.Fatalf("duplicate progress state should not re-emit, got %+v", history)
	}

	// Completion clears progress bar.
	app.busy = false
	app.batch.running = false
	app.syncWindowProgressIfChanged()
	if len(history) != 3 || history[2] != (mygo.ProgressBar{}) {
		t.Fatalf("expected cleared progress bar after completion, got %+v", history)
	}

	// File dialog / non-compression busy state with a loaded image must NOT trigger progress bar.
	app.batch = nil
	app.file = &selectedImage{path: "solo.png"}
	app.busy = true
	app.compressingSingle = false
	app.syncWindowProgressIfChanged()
	if len(history) != 3 {
		t.Fatalf("non-compression busy state must not emit progress bar, got %+v", history)
	}

	// Single image compression -> indeterminate then cleared.
	app.compressingSingle = true
	app.syncWindowProgressIfChanged()
	if len(history) != 4 || history[3].State != mygo.ProgressIndeterminate {
		t.Fatalf("expected indeterminate for single image compression, got %+v", history)
	}
	app.clearWindowProgress()
	if len(history) != 5 || history[4] != (mygo.ProgressBar{}) {
		t.Fatalf("clearWindowProgress must reset bar, got %+v", history)
	}
}
