package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type cliInstallState int

const (
	cliNotInstalled cliInstallState = iota
	cliInstalled
	cliNeedsUpdate
	cliConflict
)

func (s cliInstallState) String() string {
	switch s {
	case cliNotInstalled:
		return "not_installed"
	case cliInstalled:
		return "installed"
	case cliNeedsUpdate:
		return "needs_update"
	case cliConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

type cliInstallStatus struct {
	State       cliInstallState
	LinkPath    string
	TargetPath  string
	CurrentExec string
	InPATH      bool
	Supported   bool
}

type cliInstallEnv struct {
	executable    func() (string, error)
	candidateDirs func() []string
	pathEnv       func() string
	commandName   string
	goos          string
}

const defaultCLICommandName = "z-pic"

func defaultCLIInstallEnv() cliInstallEnv {
	return cliInstallEnv{
		executable:    defaultCLIExecutable,
		candidateDirs: defaultCLICandidateDirs,
		pathEnv:       func() string { return os.Getenv("PATH") },
		commandName:   defaultCLICommandName,
		goos:          runtime.GOOS,
	}
}

func normalizeCLIInstallEnv(env cliInstallEnv) cliInstallEnv {
	if env.commandName == "" {
		env.commandName = defaultCLICommandName
	}
	if env.executable == nil {
		env.executable = defaultCLIExecutable
	}
	if env.candidateDirs == nil {
		env.candidateDirs = defaultCLICandidateDirs
	}
	if env.pathEnv == nil {
		env.pathEnv = func() string { return os.Getenv("PATH") }
	}
	if env.goos == "" {
		env.goos = runtime.GOOS
	}
	return env
}

func defaultCLIExecutable() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func defaultCLICandidateDirs() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	var dirs []string
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		localBin := filepath.Join(home, ".local", "bin")
		info, err := os.Stat(localBin)
		if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
			dirs = append(dirs, localBin)
		}
	}
	return dirs
}

func inspectCLIInstall(env cliInstallEnv) cliInstallStatus {
	env = normalizeCLIInstallEnv(env)
	if env.goos == "windows" {
		return cliInstallStatus{
			State:     cliNotInstalled,
			Supported: false,
		}
	}
	status := cliInstallStatus{
		State:     cliNotInstalled,
		Supported: true,
	}
	if execPath, err := resolveCurrentCLIExecutable(env); err == nil {
		status.CurrentExec = execPath
	}
	if !validCLICommandName(env.commandName) {
		return status
	}

	pathVal := env.pathEnv()
	dirs := cleanCandidateDirs(env.candidateDirs())

	var updateStatus *cliInstallStatus
	var conflictStatus *cliInstallStatus
	conflictBeforeUpdate := false

	for _, dir := range dirs {
		linkPath := filepath.Join(dir, env.commandName)
		info, err := os.Lstat(linkPath)
		if err != nil {
			continue
		}
		entry := classifyCLIEntry(linkPath, info, status.CurrentExec, env.commandName, pathVal)
		switch entry.State {
		case cliInstalled:
			if conflictStatus != nil && (conflictStatus.InPATH || !entry.InPATH) {
				return *conflictStatus
			}
			return entry
		case cliNeedsUpdate:
			if updateStatus == nil {
				copied := entry
				updateStatus = &copied
				conflictBeforeUpdate = conflictStatus != nil
			}
		case cliConflict:
			if conflictStatus == nil {
				copied := entry
				conflictStatus = &copied
			}
		}
	}

	if updateStatus != nil && conflictStatus != nil {
		if conflictStatus.InPATH && !updateStatus.InPATH {
			return *conflictStatus
		}
		if updateStatus.InPATH && !conflictStatus.InPATH {
			return *updateStatus
		}
		if conflictBeforeUpdate {
			return *conflictStatus
		}
		return *updateStatus
	}
	if conflictStatus != nil {
		return *conflictStatus
	}
	if updateStatus != nil {
		return *updateStatus
	}
	return status
}

func installCLI(env cliInstallEnv) (cliInstallStatus, error) {
	env = normalizeCLIInstallEnv(env)
	if env.goos == "windows" {
		return cliInstallStatus{
			State:     cliNotInstalled,
			Supported: false,
		}, errors.New("Windows 暂不支持一键安装命令行工具，请手动将 z-pic 所在目录加入 PATH")
	}
	if !validCLICommandName(env.commandName) {
		return cliInstallStatus{Supported: true}, fmt.Errorf("无效的命令名称：%s", env.commandName)
	}

	currentExec, err := resolveCurrentCLIExecutable(env)
	if err != nil || currentExec == "" {
		return inspectCLIInstall(env), fmt.Errorf("无法定位当前可执行文件：%w", err)
	}
	execInfo, err := os.Stat(currentExec)
	if err != nil {
		return inspectCLIInstall(env), fmt.Errorf("当前可执行文件不可用：%w", err)
	}
	if execInfo.IsDir() {
		return inspectCLIInstall(env), fmt.Errorf("当前可执行文件路径不能是目录：%s", currentExec)
	}

	status := inspectCLIInstall(env)
	switch status.State {
	case cliInstalled:
		return status, nil
	case cliConflict:
		return status, fmt.Errorf("无法安装命令行工具：%s 已存在且不是 Z Pic 符号链接", status.LinkPath)
	case cliNeedsUpdate:
		targetDir := filepath.Dir(status.LinkPath)
		if !isWritableDir(targetDir) {
			return status, fmt.Errorf("目录 %s 不可写，无法更新 %s 符号链接", targetDir, env.commandName)
		}
		if err := ensureSafeReplaceTarget(status.LinkPath, currentExec, env.commandName); err != nil {
			return inspectCLIInstall(env), err
		}
		if err := replaceSymlinkAtomic(status.LinkPath, currentExec); err != nil {
			return inspectCLIInstall(env), err
		}
		return inspectCLIInstall(env), nil
	}

	dirs := cleanCandidateDirs(env.candidateDirs())
	targetDir, needsCreate, err := pickInstallDir(dirs, env.commandName)
	if err != nil {
		return status, err
	}
	if needsCreate {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return status, fmt.Errorf("无法创建命令安装目录 %s：%w", targetDir, err)
		}
	}

	linkPath := filepath.Join(targetDir, env.commandName)
	if err := ensureSafeReplaceTarget(linkPath, currentExec, env.commandName); err != nil {
		return inspectCLIInstall(env), err
	}
	if err := replaceSymlinkAtomic(linkPath, currentExec); err != nil {
		return inspectCLIInstall(env), err
	}
	return inspectCLIInstall(env), nil
}

func uninstallCLI(env cliInstallEnv) error {
	env = normalizeCLIInstallEnv(env)
	if env.goos == "windows" {
		return errors.New("Windows 暂不支持自动卸载命令行工具")
	}
	if !validCLICommandName(env.commandName) {
		return fmt.Errorf("无效的命令名称：%s", env.commandName)
	}

	currentExec, _ := resolveCurrentCLIExecutable(env)
	dirs := cleanCandidateDirs(env.candidateDirs())
	pathVal := env.pathEnv()

	removed := false
	var firstConflictPath string

	for _, dir := range dirs {
		linkPath := filepath.Join(dir, env.commandName)
		info, err := os.Lstat(linkPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("无法检查 %s：%w", linkPath, err)
		}
		entry := classifyCLIEntry(linkPath, info, currentExec, env.commandName, pathVal)
		if entry.State == cliConflict {
			if firstConflictPath == "" {
				firstConflictPath = linkPath
			}
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if firstConflictPath == "" {
				firstConflictPath = linkPath
			}
			continue
		}
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("无法移除符号链接 %s：%w", linkPath, err)
		}
		removed = true
	}

	if !removed && firstConflictPath != "" {
		return fmt.Errorf("无法卸载：%s 不是 Z Pic 创建的符号链接，已跳过删除", firstConflictPath)
	}
	return nil
}

func validCLICommandName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`) && filepath.Base(name) == name
}

func resolveCurrentCLIExecutable(env cliInstallEnv) (string, error) {
	execPath, err := env.executable()
	if err != nil {
		return "", err
	}
	execPath = strings.TrimSpace(execPath)
	if execPath == "" {
		return "", errors.New("可执行文件路径为空")
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(execPath), nil
}

func cleanCandidateDirs(dirs []string) []string {
	cleaned := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		cleaned = append(cleaned, dir)
	}
	return cleaned
}

func isWritableDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	file, err := os.CreateTemp(dir, ".z-pic-write-check-*")
	if err != nil {
		return false
	}
	name := file.Name()
	file.Close()
	os.Remove(name)
	return true
}

func pickInstallDir(dirs []string, commandName string) (dir string, needsCreate bool, err error) {
	var creatableFallback string
	for _, candidate := range dirs {
		info, statErr := os.Stat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			if creatableFallback == "" {
				creatableFallback = candidate
			}
			continue
		}
		if statErr != nil || !info.IsDir() {
			continue
		}
		if !isWritableDir(candidate) {
			continue
		}
		if _, lstatErr := os.Lstat(filepath.Join(candidate, commandName)); errors.Is(lstatErr, os.ErrNotExist) {
			return candidate, false, nil
		}
	}
	if creatableFallback != "" {
		return creatableFallback, true, nil
	}
	return "", false, errors.New("未找到可写的命令行工具安装目录")
}

func classifyCLIEntry(linkPath string, info os.FileInfo, currentExec, commandName, pathVal string) cliInstallStatus {
	status := cliInstallStatus{
		LinkPath:    linkPath,
		CurrentExec: currentExec,
		InPATH:      dirInPATH(filepath.Dir(linkPath), pathVal),
		Supported:   true,
	}
	if info.Mode()&os.ModeSymlink == 0 {
		status.State = cliConflict
		return status
	}
	rawTarget, err := os.Readlink(linkPath)
	if err != nil {
		status.State = cliConflict
		return status
	}
	absTarget := rawTarget
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(filepath.Dir(linkPath), absTarget)
	}
	absTarget = filepath.Clean(absTarget)

	resolvedTarget := absTarget
	if resolved, err := filepath.EvalSymlinks(linkPath); err == nil {
		resolvedTarget = filepath.Clean(resolved)
	}
	status.TargetPath = resolvedTarget

	if sameCLIExecutable(linkPath, resolvedTarget, currentExec) {
		status.State = cliInstalled
		return status
	}
	if isZPicBinaryPath(absTarget, commandName) || isZPicBinaryPath(resolvedTarget, commandName) {
		status.State = cliNeedsUpdate
		return status
	}
	status.State = cliConflict
	return status
}

func sameCLIExecutable(linkPath, resolvedTarget, currentExec string) bool {
	if currentExec == "" {
		return false
	}
	if linkStat, err1 := os.Stat(linkPath); err1 == nil {
		if execStat, err2 := os.Stat(currentExec); err2 == nil && os.SameFile(linkStat, execStat) {
			return true
		}
	}
	cleanExec := filepath.Clean(currentExec)
	if resolved, err := filepath.EvalSymlinks(cleanExec); err == nil {
		cleanExec = filepath.Clean(resolved)
	}
	return resolvedTarget != "" && resolvedTarget == cleanExec
}

func isZPicBinaryPath(targetPath, commandName string) bool {
	if targetPath == "" {
		return false
	}
	cleaned := filepath.Clean(targetPath)
	if strings.EqualFold(filepath.Base(cleaned), commandName) {
		return true
	}
	parts := strings.Split(filepath.ToSlash(cleaned), "/")
	for _, part := range parts {
		if strings.EqualFold(part, "Z Pic.app") || strings.EqualFold(part, "z-pic.app") {
			return true
		}
	}
	return false
}

func dirInPATH(dir, pathVal string) bool {
	if dir == "" || pathVal == "" {
		return false
	}
	cleanDir := filepath.Clean(dir)
	resolvedDir, resolvedErr := filepath.EvalSymlinks(cleanDir)
	if resolvedErr == nil {
		resolvedDir = filepath.Clean(resolvedDir)
	}
	dirInfo, dirStatErr := os.Stat(cleanDir)

	for _, entry := range filepath.SplitList(pathVal) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cleanEntry := filepath.Clean(entry)
		if cleanEntry == cleanDir {
			return true
		}
		if resolvedErr == nil {
			if resolvedEntry, err := filepath.EvalSymlinks(cleanEntry); err == nil && filepath.Clean(resolvedEntry) == resolvedDir {
				return true
			}
		}
		if dirStatErr == nil {
			if entryInfo, err := os.Stat(cleanEntry); err == nil && os.SameFile(dirInfo, entryInfo) {
				return true
			}
		}
	}
	return false
}

func ensureSafeReplaceTarget(linkPath, currentExec, commandName string) error {
	info, err := os.Lstat(linkPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法检查目标路径 %s：%w", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("目标路径 %s 已存在非符号链接文件，拒绝覆盖", linkPath)
	}
	entry := classifyCLIEntry(linkPath, info, currentExec, commandName, "")
	if entry.State == cliConflict {
		return fmt.Errorf("目标路径 %s 指向其他程序（%s），拒绝覆盖", linkPath, entry.TargetPath)
	}
	return nil
}

func replaceSymlinkAtomic(linkPath, target string) error {
	dir := filepath.Dir(linkPath)
	tmpPath, err := createTempSymlink(dir, target)
	if err != nil {
		return fmt.Errorf("无法创建临时符号链接：%w", err)
	}
	defer os.Remove(tmpPath)
	if err := os.Rename(tmpPath, linkPath); err != nil {
		return fmt.Errorf("无法替换符号链接 %s：%w", linkPath, err)
	}
	return nil
}

func createTempSymlink(dir, target string) (string, error) {
	var buf [8]byte
	for range 16 {
		if _, err := rand.Read(buf[:]); err != nil {
			return "", err
		}
		tmpPath := filepath.Join(dir, ".z-pic-link-"+hex.EncodeToString(buf[:]))
		err := os.Symlink(target, tmpPath)
		if err == nil {
			return tmpPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("无法生成唯一临时符号链接路径")
}
