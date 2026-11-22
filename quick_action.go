package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type quickActionState int

const (
	quickActionNotInstalled quickActionState = iota
	quickActionInstalled
	quickActionNeedsUpdate
	quickActionConflict
)

func (s quickActionState) String() string {
	switch s {
	case quickActionNotInstalled:
		return "not_installed"
	case quickActionInstalled:
		return "installed"
	case quickActionNeedsUpdate:
		return "needs_update"
	case quickActionConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

const (
	defaultQuickActionBundleName = "使用 Z Pic 压缩.workflow"
	defaultQuickActionMenuTitle  = "使用 Z Pic 压缩"
	quickActionTemplateVersion   = 1
)

type quickActionStatus struct {
	State          quickActionState
	BundlePath     string
	ExecutablePath string
	CurrentExec    string
	Supported      bool
}

type quickActionEnv struct {
	executable      func() (string, error)
	servicesDir     func() (string, error)
	refreshServices func()
	goos            string
}

func defaultQuickActionEnv() quickActionEnv {
	return quickActionEnv{
		executable:      defaultCLIExecutable,
		servicesDir:     defaultServicesDir,
		refreshServices: defaultRefreshMacOSServices,
		goos:            runtime.GOOS,
	}
}

func normalizeQuickActionEnv(env quickActionEnv) quickActionEnv {
	if env.executable == nil {
		env.executable = defaultCLIExecutable
	}
	if env.servicesDir == nil {
		env.servicesDir = defaultServicesDir
	}
	if env.refreshServices == nil {
		env.refreshServices = defaultRefreshMacOSServices
	}
	if env.goos == "" {
		env.goos = runtime.GOOS
	}
	return env
}

func defaultServicesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("无法确定用户主目录：%w", err)
	}
	return filepath.Join(home, "Library", "Services"), nil
}

func defaultRefreshMacOSServices() {
	if runtime.GOOS != "darwin" {
		return
	}
	const pbsPath = "/System/Library/CoreServices/pbs"
	if _, err := os.Stat(pbsPath); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, pbsPath, "-update").Run()
}

type quickActionPlistMeta struct {
	managed    bool
	version    int
	executable string
}

// parseQuickActionInfoPlist parses the top-level <dict> keys written by Z Pic
// in Contents/Info.plist without external plist dependencies.
func parseQuickActionInfoPlist(data []byte) (quickActionPlistMeta, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var meta quickActionPlistMeta
	dictDepth := 0
	var currentKey string

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return meta, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "dict":
				dictDepth++
			case "key":
				if dictDepth == 1 {
					var k string
					if err := dec.DecodeElement(&k, &t); err != nil {
						return meta, err
					}
					currentKey = k
				}
			case "true":
				if dictDepth == 1 && currentKey == "ZPicQuickActionManaged" {
					meta.managed = true
				}
				if dictDepth == 1 {
					currentKey = ""
				}
			case "false":
				if dictDepth == 1 && currentKey == "ZPicQuickActionManaged" {
					meta.managed = false
				}
				if dictDepth == 1 {
					currentKey = ""
				}
			case "integer":
				if dictDepth == 1 {
					var raw string
					if err := dec.DecodeElement(&raw, &t); err != nil {
						return meta, err
					}
					if currentKey == "ZPicQuickActionVersion" {
						if v, convErr := strconv.Atoi(strings.TrimSpace(raw)); convErr == nil {
							meta.version = v
						}
					}
					currentKey = ""
				}
			case "string":
				if dictDepth == 1 {
					var val string
					if err := dec.DecodeElement(&val, &t); err != nil {
						return meta, err
					}
					if currentKey == "ZPicQuickActionExecutable" {
						meta.executable = val
					}
					currentKey = ""
				}
			default:
				if dictDepth == 1 && t.Name.Local != "plist" {
					currentKey = ""
				}
			}
		case xml.EndElement:
			if t.Name.Local == "dict" && dictDepth > 0 {
				dictDepth--
			}
		}
	}
	return meta, nil
}

func inspectQuickAction(env quickActionEnv) quickActionStatus {
	env = normalizeQuickActionEnv(env)
	status := quickActionStatus{
		Supported: env.goos == "darwin",
	}
	if !status.Supported {
		return status
	}
	currentExec, _ := env.executable()
	status.CurrentExec = currentExec
	servicesDir, err := env.servicesDir()
	if err != nil || servicesDir == "" {
		return status
	}
	bundlePath := filepath.Join(servicesDir, defaultQuickActionBundleName)
	status.BundlePath = bundlePath

	info, err := os.Lstat(bundlePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.State = quickActionNotInstalled
			return status
		}
		status.State = quickActionConflict
		return status
	}
	if !info.IsDir() {
		status.State = quickActionConflict
		return status
	}

	plistBytes, err := os.ReadFile(filepath.Join(bundlePath, "Contents", "Info.plist"))
	if err != nil {
		status.State = quickActionConflict
		return status
	}
	meta, err := parseQuickActionInfoPlist(plistBytes)
	if err != nil || !meta.managed {
		status.State = quickActionConflict
		return status
	}
	status.ExecutablePath = meta.executable

	// Verify document.wflow is also present.
	if _, err := os.Stat(filepath.Join(bundlePath, "Contents", "document.wflow")); err != nil {
		status.State = quickActionNeedsUpdate
		return status
	}
	if meta.version != quickActionTemplateVersion {
		status.State = quickActionNeedsUpdate
		return status
	}
	if currentExec != "" && filepath.Clean(meta.executable) != filepath.Clean(currentExec) {
		status.State = quickActionNeedsUpdate
		return status
	}
	status.State = quickActionInstalled
	return status
}

func xmlEscapeString(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func posixShellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func buildQuickActionInfoPlist(execPath string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>NSServices</key>
	<array>
		<dict>
			<key>NSBackgroundColorName</key>
			<string>background</string>
			<key>NSIconName</key>
			<string>NSActionTemplate</string>
			<key>NSMenuItem</key>
			<dict>
				<key>default</key>
				<string>`)
	b.WriteString(xmlEscapeString(defaultQuickActionMenuTitle))
	b.WriteString(`</string>
			</dict>
			<key>NSMessage</key>
			<string>runWorkflowAsService</string>
			<key>NSRequiredContext</key>
			<dict>
				<key>NSApplicationIdentifier</key>
				<string>com.apple.finder</string>
			</dict>
			<key>NSSendFileTypes</key>
			<array>
				<string>public.image</string>
				<string>public.folder</string>
			</array>
		</dict>
	</array>
	<key>ZPicQuickActionManaged</key>
	<true/>
	<key>ZPicQuickActionVersion</key>
	<integer>`)
	b.WriteString(strconv.Itoa(quickActionTemplateVersion))
	b.WriteString(`</integer>
	<key>ZPicQuickActionExecutable</key>
	<string>`)
	b.WriteString(xmlEscapeString(execPath))
	b.WriteString(`</string>
</dict>
</plist>
`)
	return []byte(b.String())
}

func buildQuickActionShellScript(execPath string) string {
	return fmt.Sprintf(`#!/bin/sh
ZPIC_BIN=%s
if [ ! -x "$ZPIC_BIN" ]; then
  ZPIC_BIN=$(PATH="${PATH:+$PATH:}/opt/homebrew/bin:/usr/local/bin${HOME:+:$HOME/.local/bin}" command -v z-pic 2>/dev/null || true)
fi
if [ -z "$ZPIC_BIN" ] || [ ! -x "$ZPIC_BIN" ]; then
  /usr/bin/osascript -l JavaScript -e 'function run(){var a=Application.currentApplication();a.includeStandardAdditions=true;a.displayNotification("未找到 Z Pic 可执行文件，请在应用设置中重新安装访达快捷操作",{withTitle:"Z Pic"});}' >/dev/null 2>&1 || true
  exit 1
fi
OUT_FILE=$(mktemp "${TMPDIR:-/tmp}/zpic-qa-out.XXXXXX")
ERR_FILE=$(mktemp "${TMPDIR:-/tmp}/zpic-qa-err.XXXXXX")
trap 'rm -f "$OUT_FILE" "$ERR_FILE"' EXIT
"$ZPIC_BIN" compress --active-preset --json -- "$@" >"$OUT_FILE" 2>"$ERR_FILE"
STATUS=$?
/usr/bin/osascript -l JavaScript - "$STATUS" "$OUT_FILE" "$ERR_FILE" >/dev/null 2>&1 <<'JXA' || true
ObjC.import("Foundation");
function readTextFile(path) {
  if (!path) return "";
  try {
    var s = $.NSString.stringWithContentsOfFileEncodingError($(path), $.NSUTF8StringEncoding, $());
    return s && !s.isNil() ? ObjC.unwrap(s) : "";
  } catch (e) {
    return "";
  }
}
function fmtSize(n) {
  var v = Number(n) || 0;
  if (v < 0) v = 0;
  if (v >= 1024 * 1024) return (v / (1024 * 1024)).toFixed(2) + " MB";
  if (v >= 1024) return (v / 1024).toFixed(1) + " KB";
  return v + " B";
}
function run(argv) {
  var status = Number(argv[0] || "1");
  var raw = readTextFile(String(argv[1] || "")).trim();
  var errText = readTextFile(String(argv[2] || "")).trim();
  var app = Application.currentApplication();
  app.includeStandardAdditions = true;
  var opts = { withTitle: "Z Pic" };
  var body = "";
  if (raw) {
    try {
      var r = JSON.parse(raw);
      if (r.preset) opts.subtitle = "预设：" + r.preset;
      var total = Number(r.total) || 0;
      var ok = Number(r.succeeded) || 0;
      var fail = Number(r.failed) || 0;
      var unchanged = Number(r.unchanged) || 0;
      var saved = Number(r.saved_bytes) || 0;
      var pct = Number(r.saved_percent) || 0;
      if (total === 0) {
        body = "所选项目中未找到支持的图片";
      } else if (fail === 0) {
        if (saved > 0) {
          body = "已压缩 " + ok + " 张图片，节省 " + fmtSize(saved) + "（-" + pct.toFixed(1) + "%%）";
        } else if (unchanged === total) {
          body = "已检查 " + total + " 张图片，原图已是最优";
        } else {
          body = "已处理 " + ok + " 张图片";
        }
      } else if (ok > 0) {
        body = "已压缩 " + ok + " 张，失败 " + fail + " 张";
      } else {
        var firstErr = "";
        if (Array.isArray(r.files) && r.files.length > 0 && r.files[0].error) {
          firstErr = String(r.files[0].error);
        }
        body = firstErr ? ("压缩失败：" + firstErr) : ("压缩失败，共 " + fail + " 张");
      }
    } catch (e) {
      body = errText || (status === 0 ? "压缩完成" : "压缩失败");
    }
  } else {
    body = errText || (status === 0 ? "压缩完成" : "压缩失败");
  }
  app.displayNotification(body, opts);
}
JXA
exit $STATUS
`, posixShellSingleQuote(execPath))
}

func buildQuickActionDocumentWflow(execPath string) []byte {
	script := buildQuickActionShellScript(execPath)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AMApplicationBuild</key>
	<string>523</string>
	<key>AMApplicationVersion</key>
	<string>2.10</string>
	<key>AMDocumentVersion</key>
	<string>2</string>
	<key>actions</key>
	<array>
		<dict>
			<key>action</key>
			<dict>
				<key>AMAccepts</key>
				<dict>
					<key>Container</key>
					<string>List</string>
					<key>Optional</key>
					<true/>
					<key>Types</key>
					<array>
						<string>com.apple.cocoa.string</string>
					</array>
				</dict>
				<key>AMActionVersion</key>
				<string>2.0.3</string>
				<key>AMApplication</key>
				<array>
					<string>Automator</string>
				</array>
				<key>AMParameterProperties</key>
				<dict>
					<key>COMMAND_STRING</key>
					<dict/>
					<key>CheckedForUserDefaultShell</key>
					<dict/>
					<key>inputMethod</key>
					<dict/>
					<key>shell</key>
					<dict/>
					<key>source</key>
					<dict/>
				</dict>
				<key>AMProvides</key>
				<dict>
					<key>Container</key>
					<string>List</string>
					<key>Types</key>
					<array>
						<string>com.apple.cocoa.string</string>
					</array>
				</dict>
				<key>ActionBundlePath</key>
				<string>/System/Library/Automator/Run Shell Script.action</string>
				<key>ActionName</key>
				<string>Run Shell Script</string>
				<key>ActionParameters</key>
				<dict>
					<key>COMMAND_STRING</key>
					<string>`)
	b.WriteString(xmlEscapeString(script))
	b.WriteString(`</string>
					<key>CheckedForUserDefaultShell</key>
					<true/>
					<key>inputMethod</key>
					<integer>1</integer>
					<key>shell</key>
					<string>/bin/sh</string>
					<key>source</key>
					<string></string>
				</dict>
				<key>BundleIdentifier</key>
				<string>com.apple.RunShellScript</string>
				<key>CFBundleVersion</key>
				<string>2.0.3</string>
				<key>CanShowSelectedItemsWhenRun</key>
				<false/>
				<key>CanShowWhenRun</key>
				<true/>
				<key>Category</key>
				<array>
					<string>AMCategoryUtilities</string>
				</array>
				<key>Class Name</key>
				<string>RunShellScriptAction</string>
				<key>InputUUID</key>
				<string>9A62717E-4D62-4C6B-8C76-548B4423C2C1</string>
				<key>Keywords</key>
				<array>
					<string>Shell</string>
					<string>Script</string>
					<string>Command</string>
					<string>Run</string>
					<string>Unix</string>
				</array>
				<key>OutputUUID</key>
				<string>4764E677-E4D5-47FA-A923-0E6B4B5F3219</string>
				<key>UUID</key>
				<string>D75A4A64-4895-40DA-A7A7-55F4D5A98A1C</string>
				<key>UnlocalizedApplications</key>
				<array>
					<string>Automator</string>
				</array>
				<key>arguments</key>
				<dict>
					<key>0</key>
					<dict>
						<key>default value</key>
						<integer>0</integer>
						<key>name</key>
						<string>inputMethod</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>0</string>
					</dict>
					<key>1</key>
					<dict>
						<key>default value</key>
						<false/>
						<key>name</key>
						<string>CheckedForUserDefaultShell</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>1</string>
					</dict>
					<key>2</key>
					<dict>
						<key>default value</key>
						<string></string>
						<key>name</key>
						<string>source</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>2</string>
					</dict>
					<key>3</key>
					<dict>
						<key>default value</key>
						<string></string>
						<key>name</key>
						<string>COMMAND_STRING</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>3</string>
					</dict>
					<key>4</key>
					<dict>
						<key>default value</key>
						<string>/bin/sh</string>
						<key>name</key>
						<string>shell</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>4</string>
					</dict>
				</dict>
				<key>isViewVisible</key>
				<integer>1</integer>
				<key>location</key>
				<string>309.000000:253.000000</string>
				<key>nibPath</key>
				<string>/System/Library/Automator/Run Shell Script.action/Contents/Resources/Base.lproj/main.nib</string>
			</dict>
			<key>isViewVisible</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>connectors</key>
	<dict/>
	<key>workflowMetaData</key>
	<dict>
		<key>applicationBundleID</key>
		<string>com.apple.finder</string>
		<key>applicationBundleIDsByPath</key>
		<dict>
			<key>/System/Library/CoreServices/Finder.app</key>
			<string>com.apple.finder</string>
		</dict>
		<key>applicationPath</key>
		<string>/System/Library/CoreServices/Finder.app</string>
		<key>applicationPaths</key>
		<array>
			<string>/System/Library/CoreServices/Finder.app</string>
		</array>
		<key>inputTypeIdentifier</key>
		<string>com.apple.Automator.fileSystemObject</string>
		<key>outputTypeIdentifier</key>
		<string>com.apple.Automator.nothing</string>
		<key>presentationMode</key>
		<integer>15</integer>
		<key>processesInput</key>
		<integer>0</integer>
		<key>serviceApplicationBundleID</key>
		<string>com.apple.finder</string>
		<key>serviceApplicationPath</key>
		<string>/System/Library/CoreServices/Finder.app</string>
		<key>serviceInputTypeIdentifier</key>
		<string>com.apple.Automator.fileSystemObject</string>
		<key>serviceOutputTypeIdentifier</key>
		<string>com.apple.Automator.nothing</string>
		<key>serviceProcessesInput</key>
		<integer>0</integer>
		<key>systemImageName</key>
		<string>NSActionTemplate</string>
		<key>useAutomaticInputType</key>
		<integer>0</integer>
		<key>workflowTypeIdentifier</key>
		<string>com.apple.Automator.servicesMenu</string>
	</dict>
</dict>
</plist>
`)
	return []byte(b.String())
}

func installQuickAction(env quickActionEnv) (quickActionStatus, error) {
	env = normalizeQuickActionEnv(env)
	status := inspectQuickAction(env)
	if !status.Supported {
		return status, errors.New("当前平台不支持安装访达快捷操作")
	}
	if status.State == quickActionConflict {
		return status, fmt.Errorf("已存在非 Z Pic 创建的快捷操作 %s，未自动覆盖", status.BundlePath)
	}
	execPath, err := env.executable()
	if err != nil || execPath == "" {
		return status, fmt.Errorf("无法确定当前应用路径：%w", err)
	}
	servicesDir, err := env.servicesDir()
	if err != nil || servicesDir == "" {
		return status, fmt.Errorf("无法确定快捷操作目录：%w", err)
	}
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		return status, fmt.Errorf("无法创建目录 %s：%w", servicesDir, err)
	}

	tmpDir, err := os.MkdirTemp(servicesDir, ".zpic-quick-action-*")
	if err != nil {
		return status, fmt.Errorf("无法创建临时快捷操作目录：%w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return status, fmt.Errorf("无法设置快捷操作目录权限：%w", err)
	}
	contentsDir := filepath.Join(tmpDir, "Contents")
	if err := os.MkdirAll(contentsDir, 0o755); err != nil {
		return status, fmt.Errorf("无法创建快捷操作内容目录：%w", err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), buildQuickActionInfoPlist(execPath), 0o644); err != nil {
		return status, fmt.Errorf("无法写入 Info.plist：%w", err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "document.wflow"), buildQuickActionDocumentWflow(execPath), 0o644); err != nil {
		return status, fmt.Errorf("无法写入 document.wflow：%w", err)
	}

	bundlePath := filepath.Join(servicesDir, defaultQuickActionBundleName)
	var backupPath string
	if _, statErr := os.Lstat(bundlePath); statErr == nil {
		backupPath = bundlePath + ".zpic-backup-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(bundlePath, backupPath); err != nil {
			return status, fmt.Errorf("无法替换已有快捷操作：%w", err)
		}
	}
	if err := os.Rename(tmpDir, bundlePath); err != nil {
		if backupPath != "" {
			_ = os.Rename(backupPath, bundlePath)
		}
		return status, fmt.Errorf("无法安装访达快捷操作：%w", err)
	}
	cleanupTmp = false
	if backupPath != "" {
		_ = os.RemoveAll(backupPath)
	}
	env.refreshServices()
	return inspectQuickAction(env), nil
}

func uninstallQuickAction(env quickActionEnv) error {
	env = normalizeQuickActionEnv(env)
	status := inspectQuickAction(env)
	if !status.Supported {
		return errors.New("当前平台不支持卸载访达快捷操作")
	}
	switch status.State {
	case quickActionNotInstalled:
		return nil
	case quickActionConflict:
		return fmt.Errorf("%s 不是由 Z Pic 创建的快捷操作，已拒绝删除", status.BundlePath)
	}
	if err := os.RemoveAll(status.BundlePath); err != nil {
		return fmt.Errorf("卸载访达快捷操作失败：%w", err)
	}
	env.refreshServices()
	return nil
}
