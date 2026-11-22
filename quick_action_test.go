package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestQuickActionInstallUpdateUninstallLifecycle(t *testing.T) {
	servicesDir := filepath.Join(t.TempDir(), "Services")
	currentExec := filepath.Join(t.TempDir(), "Z Pic.app", "Contents", "MacOS", "z-pic")
	refreshed := 0
	env := quickActionEnv{
		executable:      func() (string, error) { return currentExec, nil },
		servicesDir:     func() (string, error) { return servicesDir, nil },
		refreshServices: func() { refreshed++ },
		goos:            "darwin",
	}

	status := inspectQuickAction(env)
	if !status.Supported || status.State != quickActionNotInstalled {
		t.Fatalf("initial status = %+v, want supported & not_installed", status)
	}

	installed, err := installQuickAction(env)
	if err != nil {
		t.Fatalf("installQuickAction failed: %v", err)
	}
	if installed.State != quickActionInstalled || installed.ExecutablePath != currentExec || refreshed != 1 {
		t.Fatalf("unexpected installed status=%+v refreshed=%d", installed, refreshed)
	}

	infoPlist := filepath.Join(installed.BundlePath, "Contents", "Info.plist")
	docWflow := filepath.Join(installed.BundlePath, "Contents", "document.wflow")
	if runtime.GOOS == "darwin" {
		for _, plistPath := range []string{infoPlist, docWflow} {
			out, err := exec.Command("plutil", "-lint", plistPath).CombinedOutput()
			if err != nil {
				t.Fatalf("plutil -lint %s failed: %v\n%s", plistPath, err, string(out))
			}
		}
	}

	// Moving the executable triggers needs_update and atomic reinstall updates it.
	movedExec := filepath.Join(t.TempDir(), "Applications", "Z Pic & Co's.app", "Contents", "MacOS", "z-pic")
	env.executable = func() (string, error) { return movedExec, nil }
	stale := inspectQuickAction(env)
	if stale.State != quickActionNeedsUpdate {
		t.Fatalf("expected needs_update after app move, got %+v", stale)
	}

	updated, err := installQuickAction(env)
	if err != nil {
		t.Fatalf("reinstall quick action failed: %v", err)
	}
	if updated.State != quickActionInstalled || updated.ExecutablePath != movedExec || refreshed != 2 {
		t.Fatalf("unexpected updated status=%+v refreshed=%d", updated, refreshed)
	}
	if runtime.GOOS == "darwin" {
		for _, plistPath := range []string{infoPlist, docWflow} {
			out, err := exec.Command("plutil", "-lint", plistPath).CombinedOutput()
			if err != nil {
				t.Fatalf("plutil -lint after special chars failed (%s): %v\n%s", plistPath, err, string(out))
			}
		}
	}

	// Verify generated shell script passes /bin/sh -n syntax validation with special chars
	// and uses temp-file handoff, extended PATH fallback, and `--` separator.
	script := buildQuickActionShellScript(movedExec)
	for _, want := range []string{
		"/opt/homebrew/bin:/usr/local/bin",
		"$HOME/.local/bin",
		`compress --active-preset --json -- "$@" >"$OUT_FILE" 2>"$ERR_FILE"`,
		`"$STATUS" "$OUT_FILE" "$ERR_FILE"`,
		"readTextFile(",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("generated shell script missing %q", want)
		}
	}
	if strings.Contains(script, `"$JSON_OUT"`) {
		t.Fatal("generated shell script must not pass full JSON payload via argv")
	}
	if runtime.GOOS != "windows" {
		cmd := exec.Command("/bin/sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated shell script failed /bin/sh -n: %v\n%s", err, string(out))
		}
	}

	if err := uninstallQuickAction(env); err != nil {
		t.Fatalf("uninstallQuickAction failed: %v", err)
	}
	if refreshed != 3 {
		t.Fatalf("expected refresh on uninstall, got %d", refreshed)
	}
	after := inspectQuickAction(env)
	if after.State != quickActionNotInstalled {
		t.Fatalf("expected not_installed after uninstall, got %+v", after)
	}
	// Idempotent second uninstall.
	if err := uninstallQuickAction(env); err != nil {
		t.Fatalf("second uninstall should be nil, got %v", err)
	}
}

func TestQuickActionConflictProtectionAndUnsupportedPlatform(t *testing.T) {
	t.Run("unmanaged workflow directory is never overwritten or removed", func(t *testing.T) {
		servicesDir := filepath.Join(t.TempDir(), "Services")
		bundlePath := filepath.Join(servicesDir, defaultQuickActionBundleName)
		if err := os.MkdirAll(filepath.Join(bundlePath, "Contents"), 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(bundlePath, "Contents", "Info.plist")
		if err := os.WriteFile(sentinel, []byte(`<?xml version="1.0"?><plist version="1.0"><dict></dict></plist>`), 0o644); err != nil {
			t.Fatal(err)
		}
		env := quickActionEnv{
			executable:      func() (string, error) { return "/Applications/Z Pic.app/Contents/MacOS/z-pic", nil },
			servicesDir:     func() (string, error) { return servicesDir, nil },
			refreshServices: func() {},
			goos:            "darwin",
		}
		status := inspectQuickAction(env)
		if status.State != quickActionConflict {
			t.Fatalf("expected conflict, got %+v", status)
		}
		if _, err := installQuickAction(env); err == nil {
			t.Fatal("installQuickAction must refuse to overwrite unmanaged workflow")
		}
		if err := uninstallQuickAction(env); err == nil {
			t.Fatal("uninstallQuickAction must refuse to delete unmanaged workflow")
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("unmanaged workflow must remain intact: %v", err)
		}
	})

	t.Run("non-darwin platform is reported unsupported", func(t *testing.T) {
		env := quickActionEnv{
			executable:      func() (string, error) { return "/usr/bin/z-pic", nil },
			servicesDir:     func() (string, error) { return t.TempDir(), nil },
			refreshServices: func() {},
			goos:            "linux",
		}
		status := inspectQuickAction(env)
		if status.Supported {
			t.Fatalf("expected Supported=false on linux, got %+v", status)
		}
		if _, err := installQuickAction(env); err == nil {
			t.Fatal("installQuickAction should fail on non-darwin")
		}
		if err := uninstallQuickAction(env); err == nil {
			t.Fatal("uninstallQuickAction should fail on non-darwin")
		}
	})
}

func TestSettingsGeneralQuickActionPanelActions(t *testing.T) {
	servicesDir := filepath.Join(t.TempDir(), "Services")
	app := testSelectedApplication()
	updates := make(chan func(), 4)
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	blockRefresh := make(chan struct{})
	refreshEntered := make(chan struct{}, 2)
	refreshCalls := 0
	app.quickActionEnv = quickActionEnv{
		executable:  func() (string, error) { return "/Applications/Z Pic.app/Contents/MacOS/z-pic", nil },
		servicesDir: func() (string, error) { return servicesDir, nil },
		refreshServices: func() {
			refreshCalls++
			select {
			case refreshEntered <- struct{}{}:
			default:
			}
			<-blockRefresh
		},
		goos: "darwin",
	}
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, 724, 900)
	if !tester.HasText("访达右键快捷操作") {
		t.Fatal("general settings must render Finder Quick Action panel")
	}

	// Install runs asynchronously so UI thread is never blocked by pbs -update.
	app.runInstallQuickAction()
	if !app.editor.quickActionBusy {
		t.Fatal("expected quickActionBusy=true immediately after runInstallQuickAction")
	}
	// Concurrent second click while busy must be ignored.
	app.runInstallQuickAction()
	select {
	case <-refreshEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for background refreshServices")
	}
	close(blockRefresh)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for install completion callback")
	}
	tester.Frame()
	if app.editor.quickActionBusy || app.editor.quickActionStatus.State != quickActionInstalled || app.editor.quickActionFailed {
		t.Fatalf("runInstallQuickAction failed: busy=%v status=%+v msg=%q", app.editor.quickActionBusy, app.editor.quickActionStatus, app.editor.quickActionMessage)
	}
	if refreshCalls != 1 {
		t.Fatalf("expected exactly 1 refresh call despite duplicate click, got %d", refreshCalls)
	}

	app.runUninstallQuickAction()
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for uninstall completion callback")
	}
	tester.Frame()
	if app.editor.quickActionBusy || app.editor.quickActionStatus.State != quickActionNotInstalled || app.editor.quickActionFailed {
		t.Fatalf("runUninstallQuickAction failed: busy=%v status=%+v msg=%q", app.editor.quickActionBusy, app.editor.quickActionStatus, app.editor.quickActionMessage)
	}
}
