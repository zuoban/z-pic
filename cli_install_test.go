package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func createTestExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho z-pic\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}

func assertNoCLITempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".z-pic-") {
			t.Fatalf("unexpected leftover temp file in %s: %s", dir, entry.Name())
		}
	}
}

func TestCLIInstallFreshAndAlreadyInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	tmp := t.TempDir()
	execPath := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := cliInstallEnv{
		executable:    func() (string, error) { return execPath, nil },
		candidateDirs: func() []string { return []string{binDir} },
		pathEnv:       func() string { return binDir + string(os.PathListSeparator) + "/usr/bin" },
		commandName:   "z-pic",
	}

	status := inspectCLIInstall(env)
	if !status.Supported {
		t.Fatal("expected CLI install to be supported on unix")
	}
	if status.State != cliNotInstalled {
		t.Fatalf("expected cliNotInstalled, got %v", status.State)
	}
	if status.CurrentExec != execPath {
		t.Fatalf("CurrentExec = %q, want %q", status.CurrentExec, execPath)
	}
	if status.LinkPath != "" || status.TargetPath != "" || status.InPATH {
		t.Fatalf("unexpected non-empty fresh status: %+v", status)
	}

	installed, err := installCLI(env)
	if err != nil {
		t.Fatalf("installCLI failed: %v", err)
	}
	wantLink := filepath.Join(binDir, "z-pic")
	if installed.State != cliInstalled {
		t.Fatalf("expected cliInstalled, got %v", installed.State)
	}
	if installed.LinkPath != wantLink {
		t.Fatalf("LinkPath = %q, want %q", installed.LinkPath, wantLink)
	}
	if installed.TargetPath != execPath {
		t.Fatalf("TargetPath = %q, want %q", installed.TargetPath, execPath)
	}
	if !installed.InPATH {
		t.Fatal("expected InPATH to be true")
	}
	assertNoCLITempFiles(t, binDir)

	info, err := os.Lstat(wantLink)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s, got mode %v", wantLink, info.Mode())
	}

	// Re-inspecting and re-installing should be idempotent.
	rechecked := inspectCLIInstall(env)
	if rechecked != installed {
		t.Fatalf("rechecked status = %+v, want %+v", rechecked, installed)
	}
	again, err := installCLI(env)
	if err != nil {
		t.Fatalf("second installCLI failed: %v", err)
	}
	if again != installed {
		t.Fatalf("second install status = %+v, want %+v", again, installed)
	}
	assertNoCLITempFiles(t, binDir)
}

func TestCLIInstallUpdatesOutdatedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	tests := []struct {
		name      string
		setupLink func(t *testing.T, tmp, binDir string) string
	}{
		{
			name: "broken symlink to old z-pic path",
			setupLink: func(t *testing.T, tmp, binDir string) string {
				oldTarget := filepath.Join(tmp, "deleted-old-release", "z-pic")
				if err := os.Symlink(oldTarget, filepath.Join(binDir, "z-pic")); err != nil {
					t.Fatal(err)
				}
				return oldTarget
			},
		},
		{
			name: "existing symlink inside old Z Pic.app bundle",
			setupLink: func(t *testing.T, tmp, binDir string) string {
				oldExec := createTestExecutable(t, filepath.Join(tmp, "Old", "Z Pic.app", "Contents", "MacOS"), "Z Pic")
				if err := os.Symlink(oldExec, filepath.Join(binDir, "z-pic")); err != nil {
					t.Fatal(err)
				}
				return oldExec
			},
		},
		{
			name: "relative symlink to older z-pic binary",
			setupLink: func(t *testing.T, tmp, binDir string) string {
				oldExec := createTestExecutable(t, filepath.Join(tmp, "old-bin"), "z-pic")
				rel, err := filepath.Rel(binDir, oldExec)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(rel, filepath.Join(binDir, "z-pic")); err != nil {
					t.Fatal(err)
				}
				return oldExec
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			currentExec := createTestExecutable(t, filepath.Join(tmp, "current", "Z Pic.app", "Contents", "MacOS"), "z-pic")
			binDir := filepath.Join(tmp, "bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			oldTarget := tc.setupLink(t, tmp, binDir)

			env := cliInstallEnv{
				executable:    func() (string, error) { return currentExec, nil },
				candidateDirs: func() []string { return []string{binDir} },
				pathEnv:       func() string { return binDir },
				commandName:   "z-pic",
			}

			before := inspectCLIInstall(env)
			if before.State != cliNeedsUpdate {
				t.Fatalf("expected cliNeedsUpdate, got %v (status=%+v)", before.State, before)
			}
			if before.LinkPath != filepath.Join(binDir, "z-pic") {
				t.Fatalf("LinkPath = %q, want %q", before.LinkPath, filepath.Join(binDir, "z-pic"))
			}
			if before.TargetPath != filepath.Clean(oldTarget) {
				t.Fatalf("TargetPath = %q, want %q", before.TargetPath, filepath.Clean(oldTarget))
			}

			after, err := installCLI(env)
			if err != nil {
				t.Fatalf("installCLI update failed: %v", err)
			}
			if after.State != cliInstalled {
				t.Fatalf("expected cliInstalled after update, got %v", after.State)
			}
			if after.TargetPath != currentExec {
				t.Fatalf("updated TargetPath = %q, want %q", after.TargetPath, currentExec)
			}
			assertNoCLITempFiles(t, binDir)
		})
	}
}

func TestCLIInstallConflictWithRegularFileAndUnrelatedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	t.Run("non-symlink regular file", func(t *testing.T) {
		tmp := t.TempDir()
		currentExec := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")
		binDir := filepath.Join(tmp, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		conflictPath := filepath.Join(binDir, "z-pic")
		const content = "#!/bin/sh\necho custom-tool\n"
		if err := os.WriteFile(conflictPath, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}

		env := cliInstallEnv{
			executable:    func() (string, error) { return currentExec, nil },
			candidateDirs: func() []string { return []string{binDir} },
			pathEnv:       func() string { return binDir },
			commandName:   "z-pic",
		}

		status := inspectCLIInstall(env)
		if status.State != cliConflict {
			t.Fatalf("expected cliConflict, got %v", status.State)
		}
		if status.LinkPath != conflictPath {
			t.Fatalf("LinkPath = %q, want %q", status.LinkPath, conflictPath)
		}

		installStatus, err := installCLI(env)
		if err == nil {
			t.Fatal("expected installCLI to reject overwriting regular file")
		}
		if installStatus.State != cliConflict {
			t.Fatalf("expected cliConflict status on error, got %v", installStatus.State)
		}

		if err := uninstallCLI(env); err == nil {
			t.Fatal("expected uninstallCLI to refuse deleting regular file")
		}

		got, readErr := os.ReadFile(conflictPath)
		if readErr != nil || string(got) != content {
			t.Fatalf("regular file was modified or removed: content=%q err=%v", got, readErr)
		}
		assertNoCLITempFiles(t, binDir)
	})

	t.Run("unrelated symlink", func(t *testing.T) {
		tmp := t.TempDir()
		currentExec := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")
		otherTool := createTestExecutable(t, filepath.Join(tmp, "other"), "unrelated-tool")
		binDir := filepath.Join(tmp, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(binDir, "z-pic")
		if err := os.Symlink(otherTool, linkPath); err != nil {
			t.Fatal(err)
		}

		env := cliInstallEnv{
			executable:    func() (string, error) { return currentExec, nil },
			candidateDirs: func() []string { return []string{binDir} },
			pathEnv:       func() string { return binDir },
			commandName:   "z-pic",
		}

		status := inspectCLIInstall(env)
		if status.State != cliConflict {
			t.Fatalf("expected cliConflict for unrelated symlink, got %v", status.State)
		}
		if status.TargetPath != otherTool {
			t.Fatalf("TargetPath = %q, want %q", status.TargetPath, otherTool)
		}

		if _, err := installCLI(env); err == nil {
			t.Fatal("expected installCLI to refuse overwriting unrelated symlink")
		}
		if err := uninstallCLI(env); err == nil {
			t.Fatal("expected uninstallCLI to refuse deleting unrelated symlink")
		}

		target, err := os.Readlink(linkPath)
		if err != nil || target != otherTool {
			t.Fatalf("unrelated symlink was modified or removed: target=%q err=%v", target, err)
		}
	})
}

func TestCLIInstallUninstallSafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	tmp := t.TempDir()
	currentExec := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := cliInstallEnv{
		executable:    func() (string, error) { return currentExec, nil },
		candidateDirs: func() []string { return []string{binDir} },
		pathEnv:       func() string { return binDir },
		commandName:   "z-pic",
	}

	// Uninstall when nothing is installed should succeed without error.
	if err := uninstallCLI(env); err != nil {
		t.Fatalf("uninstallCLI on clean dir failed: %v", err)
	}

	// Install and then uninstall removes the symlink cleanly.
	if _, err := installCLI(env); err != nil {
		t.Fatalf("installCLI failed: %v", err)
	}
	if err := uninstallCLI(env); err != nil {
		t.Fatalf("uninstallCLI failed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "z-pic")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink to be removed, err=%v", err)
	}
	if status := inspectCLIInstall(env); status.State != cliNotInstalled {
		t.Fatalf("expected cliNotInstalled after uninstall, got %v", status.State)
	}

	// Uninstall also removes broken/outdated z-pic symlinks.
	brokenTarget := filepath.Join(tmp, "removed-app", "Z Pic.app", "Contents", "MacOS", "Z Pic")
	if err := os.Symlink(brokenTarget, filepath.Join(binDir, "z-pic")); err != nil {
		t.Fatal(err)
	}
	if err := uninstallCLI(env); err != nil {
		t.Fatalf("uninstallCLI failed on outdated z-pic symlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "z-pic")); !os.IsNotExist(err) {
		t.Fatalf("expected outdated z-pic symlink to be removed, err=%v", err)
	}
}

func TestCLIInstallCreatesLocalBinAndDetectsPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	tmp := t.TempDir()
	currentExec := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")

	readOnlyDir := filepath.Join(tmp, "readonly-bin")
	if err := os.MkdirAll(readOnlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o755)
	})

	localBin := filepath.Join(tmp, "home", ".local", "bin")
	var candidateDirs []string
	if os.Geteuid() != 0 {
		candidateDirs = []string{readOnlyDir, localBin}
	} else {
		candidateDirs = []string{localBin}
	}

	pathValue := "/usr/bin" + string(os.PathListSeparator) + "/bin"
	env := cliInstallEnv{
		executable:    func() (string, error) { return currentExec, nil },
		candidateDirs: func() []string { return candidateDirs },
		pathEnv:       func() string { return pathValue },
		commandName:   "z-pic",
	}

	status, err := installCLI(env)
	if err != nil {
		t.Fatalf("installCLI fallback to .local/bin failed: %v", err)
	}
	if status.State != cliInstalled {
		t.Fatalf("expected cliInstalled, got %v", status.State)
	}
	wantLink := filepath.Join(localBin, "z-pic")
	if status.LinkPath != wantLink {
		t.Fatalf("LinkPath = %q, want %q", status.LinkPath, wantLink)
	}
	if status.InPATH {
		t.Fatal("expected InPATH to be false when .local/bin is absent from PATH")
	}
	info, err := os.Stat(localBin)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %s to be created as a directory, err=%v", localBin, err)
	}
	if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
		t.Fatalf("unexpected permissions on %s: %o", localBin, perm)
	}

	// Adding localBin to PATH makes InPATH true.
	pathValue = "/usr/bin" + string(os.PathListSeparator) + localBin
	withPath := inspectCLIInstall(env)
	if !withPath.InPATH {
		t.Fatal("expected InPATH to be true when .local/bin is in PATH")
	}

	// Symlinked alias of localBin in PATH also detects InPATH = true.
	aliasDir := filepath.Join(tmp, "path-alias")
	if err := os.Symlink(localBin, aliasDir); err != nil {
		t.Fatal(err)
	}
	pathValue = aliasDir
	withAlias := inspectCLIInstall(env)
	if !withAlias.InPATH {
		t.Fatal("expected InPATH to be true when a symlink alias of .local/bin is in PATH")
	}
}

func TestCLIInstallWindowsUnsupported(t *testing.T) {
	env := cliInstallEnv{
		executable:    func() (string, error) { return `C:\Program Files\Z Pic\z-pic.exe`, nil },
		candidateDirs: func() []string { return nil },
		pathEnv:       func() string { return "" },
		commandName:   "z-pic",
		goos:          "windows",
	}

	status := inspectCLIInstall(env)
	if status.Supported {
		t.Fatal("expected Supported=false on windows")
	}
	if status.State != cliNotInstalled {
		t.Fatalf("expected cliNotInstalled on windows, got %v", status.State)
	}

	installStatus, err := installCLI(env)
	if err == nil {
		t.Fatal("expected error from installCLI on windows")
	}
	if installStatus.Supported {
		t.Fatal("expected Supported=false from installCLI on windows")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected clear Windows message, got %q", err.Error())
	}

	if err := uninstallCLI(env); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected clear Windows error from uninstallCLI, got %v", err)
	}
}

func TestCLIInstallDefaultEnv(t *testing.T) {
	env := defaultCLIInstallEnv()
	if env.commandName != "z-pic" {
		t.Fatalf("default commandName = %q, want z-pic", env.commandName)
	}
	if env.executable == nil || env.candidateDirs == nil || env.pathEnv == nil {
		t.Fatal("defaultCLIInstallEnv must populate all helper functions")
	}
	execPath, err := env.executable()
	if err != nil || execPath == "" {
		t.Fatalf("default executable helper failed: path=%q err=%v", execPath, err)
	}
}

func TestCLIInspectAndDefaultCandidateDirsDoNotWriteTempFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix candidate dir test")
	}

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// 1. When ~/.local/bin does not exist yet, defaultCLICandidateDirs includes it without creating it.
	localBin := filepath.Join(fakeHome, ".local", "bin")
	dirs := defaultCLICandidateDirs()
	if !slices.Contains(dirs, localBin) {
		t.Fatalf("defaultCLICandidateDirs() = %v, want %q included when missing", dirs, localBin)
	}
	if _, err := os.Stat(localBin); !os.IsNotExist(err) {
		t.Fatalf("defaultCLICandidateDirs must not create %s, err=%v", localBin, err)
	}

	// 2. When ~/.local/bin exists (writable or read-only), defaultCLICandidateDirs and inspectCLIInstall
	//    must not write temp files or modify directory mtime.
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(localBin, past, past); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(localBin)
	if err != nil {
		t.Fatal(err)
	}

	writableDirs := defaultCLICandidateDirs()
	if !slices.Contains(writableDirs, localBin) {
		t.Fatalf("defaultCLICandidateDirs() = %v, want writable %q included", writableDirs, localBin)
	}
	execPath := createTestExecutable(t, filepath.Join(t.TempDir(), "app"), "z-pic")
	_ = inspectCLIInstall(cliInstallEnv{
		executable:    func() (string, error) { return execPath, nil },
		candidateDirs: func() []string { return []string{localBin} },
		pathEnv:       func() string { return localBin },
		commandName:   "z-pic",
	})
	afterInfo, err := os.Stat(localBin)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("inspectCLIInstall/defaultCLICandidateDirs modified dir mtime: before=%v after=%v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
	assertNoCLITempFiles(t, localBin)

	// 3. Even when ~/.local/bin is read-only (0555), defaultCLICandidateDirs still includes it without touching it.
	if err := os.Chmod(localBin, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(localBin, 0o755) })
	roDirs := defaultCLICandidateDirs()
	if !slices.Contains(roDirs, localBin) {
		t.Fatalf("defaultCLICandidateDirs() = %v, want read-only %q included", roDirs, localBin)
	}
}

func TestSettingsCLIConflictShowsRefreshButtonAndClearsMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink CLI install tests require unix")
	}

	tmp := t.TempDir()
	execPath := createTestExecutable(t, filepath.Join(tmp, "app"), "z-pic")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conflictPath := filepath.Join(binDir, "z-pic")
	if err := os.WriteFile(conflictPath, []byte("other binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	app := testSelectedApplication()
	app.cliInstallEnv = cliInstallEnv{
		executable:    func() (string, error) { return execPath, nil },
		candidateDirs: func() []string { return []string{binDir} },
		pathEnv:       func() string { return binDir },
		commandName:   "z-pic",
	}
	app.setSettingsOpen(true)
	if app.editor.cliStatus.State != cliConflict {
		t.Fatalf("expected cliConflict, got %v", app.editor.cliStatus.State)
	}
	app.editor.cliMessage = "之前的冲突错误"
	app.editor.cliFailed = true

	tester := ui.NewTester(app.settingsView, 724, 960)
	if !tester.HasText("重新检测") {
		t.Fatal("expected 重新检测 button for cliConflict state")
	}
	if tester.HasText("安装命令行工具") {
		t.Fatal("cliConflict must not render 安装命令行工具 button")
	}

	if err := os.Remove(conflictPath); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("重新检测"); err != nil {
		t.Fatalf("clicking 重新检测 failed: %v", err)
	}
	if app.editor.cliStatus.State != cliNotInstalled {
		t.Fatalf("expected cliNotInstalled after refresh, got %v", app.editor.cliStatus.State)
	}
	if app.editor.cliMessage != "" || app.editor.cliFailed {
		t.Fatalf("expected stale cliMessage cleared after refresh, got msg=%q failed=%v", app.editor.cliMessage, app.editor.cliFailed)
	}
	if !tester.HasText("安装命令行工具") {
		t.Fatal("expected 安装命令行工具 button after conflict is resolved")
	}
}
