package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"

	"zpic/internal/compress"
)

// folderWatchSettings remains comparable for direct equality checks and
// backward compatibility with single-rule settings.json files.
type folderWatchSettings struct {
	InputDir  string `json:"input_dir,omitempty"`
	OutputDir string `json:"output_dir,omitempty"`
	Preset    string `json:"preset,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	AutoStart bool   `json:"auto_start,omitempty"`
	Notify    bool   `json:"notify,omitempty"`
}

type folderWatchRule struct {
	InputDir  string `json:"input_dir,omitempty"`
	OutputDir string `json:"output_dir,omitempty"`
	Preset    string `json:"preset,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	AutoStart bool   `json:"auto_start,omitempty"`
}

func (s folderWatchSettings) primaryRule() folderWatchRule {
	return folderWatchRule{
		InputDir:  s.InputDir,
		OutputDir: s.OutputDir,
		Preset:    s.Preset,
		Recursive: s.Recursive,
		AutoStart: s.AutoStart,
	}
}

func (s *folderWatchSettings) applyPrimaryRule(rule folderWatchRule) {
	s.InputDir = rule.InputDir
	s.OutputDir = rule.OutputDir
	s.Preset = rule.Preset
	s.Recursive = rule.Recursive
	s.AutoStart = rule.AutoStart
}

type desktopFolderWatch struct {
	settings     folderWatchSettings
	extraRules   []folderWatchRule
	session      *desktopWatchSession
	ruleSessions map[int]*desktopWatchSession
	last         folderWatchOutcome
	err          error
}

// The UI owns the session fields. The worker only signals done and posts updates.
type desktopWatchSession struct {
	ruleIndex int
	settings  folderWatchSettings
	rule      folderWatchRule
	cancel    context.CancelFunc
	done      chan struct{}
	ready     bool
	stopping  bool
	last      folderWatchOutcome
	err       error
}

func (s *application) allWatchRules() []folderWatchRule {
	rules := make([]folderWatchRule, 0, 1+len(s.watch.extraRules))
	rules = append(rules, s.watch.settings.primaryRule())
	rules = append(rules, s.watch.extraRules...)
	return rules
}

func (s *application) watchSessionForRule(ruleIndex int) *desktopWatchSession {
	if ruleIndex == 0 {
		return s.watch.session
	}
	if s.watch.ruleSessions == nil {
		return nil
	}
	return s.watch.ruleSessions[ruleIndex]
}

func (s *application) setWatchSessionForRule(ruleIndex int, session *desktopWatchSession) {
	if ruleIndex == 0 {
		s.watch.session = session
		return
	}
	if session == nil {
		delete(s.watch.ruleSessions, ruleIndex)
		return
	}
	if s.watch.ruleSessions == nil {
		s.watch.ruleSessions = make(map[int]*desktopWatchSession)
	}
	s.watch.ruleSessions[ruleIndex] = session
}

func (s *application) activeFolderWatchSessions() []*desktopWatchSession {
	var list []*desktopWatchSession
	if s.watch.session != nil {
		list = append(list, s.watch.session)
	}
	if len(s.watch.ruleSessions) > 0 {
		keys := make([]int, 0, len(s.watch.ruleSessions))
		for k := range s.watch.ruleSessions {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if sess := s.watch.ruleSessions[k]; sess != nil {
				list = append(list, sess)
			}
		}
	}
	return list
}

func (s *application) activeFolderWatchCount() int {
	return len(s.activeFolderWatchSessions())
}

func (s *application) anyFolderWatchRunning() bool {
	return s.activeFolderWatchCount() > 0
}

func (s *application) hasActiveWatchSessionFrom(ruleIndex int) bool {
	if ruleIndex <= 0 && s.watch.session != nil {
		return true
	}
	for k, sess := range s.watch.ruleSessions {
		if k >= ruleIndex && sess != nil {
			return true
		}
	}
	return false
}

func (s *application) folderWatchPreset(name string) (compressionPreset, bool) {
	if name == "" {
		name = s.scheme
	}
	for _, preset := range s.presets {
		if preset.name == name {
			return preset, true
		}
	}
	return compressionPreset{}, false
}

func (s *application) startFolderWatch(settings folderWatchSettings) error {
	return s.startWatchRuleWithNotify(0, settings.primaryRule(), settings.Notify, true)
}

func (s *application) startWatchRule(ruleIndex int, rule folderWatchRule) error {
	return s.startWatchRuleWithNotify(ruleIndex, rule, s.watch.settings.Notify, true)
}

func (s *application) startWatchRuleWithNotify(ruleIndex int, rule folderWatchRule, notifyEnabled bool, persist bool) error {
	if s.closed {
		return errors.New("应用正在关闭")
	}
	if s.watchSessionForRule(ruleIndex) != nil {
		return errors.New("请等待当前监听停止")
	}
	if rule.InputDir == "" || rule.OutputDir == "" {
		return errors.New("请选择输入目录和输出目录")
	}
	preset, ok := s.folderWatchPreset(rule.Preset)
	if !ok {
		return errors.New("所选监听预设已不可用，请重新选择")
	}
	rule.Preset = preset.name
	settings := folderWatchSettings{
		InputDir:  rule.InputDir,
		OutputDir: rule.OutputDir,
		Preset:    rule.Preset,
		Recursive: rule.Recursive,
		AutoStart: rule.AutoStart,
		Notify:    notifyEnabled,
	}
	request := folderWatchRequest{
		InputDir:  rule.InputDir,
		OutputDir: rule.OutputDir,
		Options:   preset.compressionOptions(),
		Recursive: rule.Recursive,
		AcquireMemory: func(ctx context.Context, info compress.ImageInfo) (func(), error) {
			return s.acquireImageMemory(ctx, info, false)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &desktopWatchSession{
		ruleIndex: ruleIndex,
		settings:  settings,
		rule:      rule,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	s.setWatchSessionForRule(ruleIndex, session)
	if ruleIndex == 0 {
		s.watch.last, s.watch.err = folderWatchOutcome{}, nil
	} else {
		s.watch.err = nil
	}

	go func() {
		err := runFolderWatch(ctx, request, folderWatchNotifications{
			Ready: func(folderWatchReady) {
				s.updateFolderWatch(session, func() {
					if session.stopping {
						return
					}
					session.ready = true
					if persist {
						if err := s.rememberFolderWatchRule(ruleIndex, rule, notifyEnabled); err != nil {
							session.err = fmt.Errorf("无法记住监听设置：%w", err)
							s.watch.err = session.err
						}
					}
					s.refreshTrayMenu()
				})
			},
			Image: func(outcome folderWatchOutcome) {
				s.updateFolderWatch(session, func() {
					session.last = outcome
					s.watch.last = outcome
					s.notifyFolderWatchOutcome(outcome)
				})
			},
		})
		cancel()
		close(session.done)
		s.updateFolderWatch(session, func() {
			if !session.stopping && err != nil {
				session.err = err
				s.watch.err = err
			}
			s.setWatchSessionForRule(ruleIndex, nil)
			s.refreshTrayMenu()
		})
	}()
	s.invalidateFolderWatchViews()
	s.refreshTrayMenu()
	return nil
}

func (s *application) rememberFolderWatchSettings(settings folderWatchSettings) error {
	return s.rememberFolderWatchRule(0, settings.primaryRule(), settings.Notify)
}

func persistedFolderWatchRules(rules []folderWatchRule) []folderWatchRule {
	var persisted []folderWatchRule
	for _, rule := range rules {
		if rule.InputDir == "" && rule.OutputDir == "" {
			continue
		}
		persisted = append(persisted, rule)
	}
	return persisted
}

func (s *application) rememberFolderWatchRule(ruleIndex int, rule folderWatchRule, notifyEnabled bool) error {
	nextWatch := s.watch.settings
	nextWatch.Notify = notifyEnabled
	nextExtra := slices.Clone(s.watch.extraRules)
	if ruleIndex == 0 {
		nextWatch.applyPrimaryRule(rule)
	} else {
		extraIdx := ruleIndex - 1
		if extraIdx >= 0 {
			for len(nextExtra) <= extraIdx {
				nextExtra = append(nextExtra, folderWatchRule{})
			}
			nextExtra[extraIdx] = rule
		}
	}
	if nextWatch == s.watch.settings && slices.Equal(nextExtra, s.watch.extraRules) {
		return nil
	}
	if s.settingsError != "" {
		return errors.New("请先在设置中保存以修复设置文件")
	}
	if s.settingsFile != "" {
		if err := s.saveSettingsSnapshotFull(s.presets, s.scheme, s.batchOutputDir, s.effectiveThemeMode(), s.autoCompress, s.backupToTrash, s.keepInMenuBar, s.downloadDir, nextWatch, persistedFolderWatchRules(nextExtra)); err != nil {
			return err
		}
	}
	s.watch.settings = nextWatch
	s.watch.extraRules = nextExtra
	return nil
}

func (s *application) notifyFolderWatchOutcome(outcome folderWatchOutcome) {
	if !s.watch.settings.Notify {
		return
	}
	name := filepath.Base(outcome.Input)
	title := "Z Pic 文件夹监听"
	var body string
	switch {
	case outcome.Err != nil:
		body = fmt.Sprintf("压缩失败：%s（%v）", name, outcome.Err)
	case outcome.TargetSizeMissed:
		body = fmt.Sprintf("未达目标体积：%s（%s / %s，已保存）", name,
			formatSize(outcome.OutputBytes), compress.FormatTargetSize(outcome.TargetBytes))
	case outcome.Unchanged:
		body = fmt.Sprintf("体积未变：%s（已保存副本）", name)
	default:
		body = fmt.Sprintf("已压缩 %s · %s → %s", name, formatSize(outcome.OriginalBytes), formatSize(outcome.OutputBytes))
	}
	if s.sendNotification != nil {
		s.sendNotification(title, body)
		return
	}
	if !mygo.App.IsReady() || !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:     "z-pic-watch-latest",
		Title:  title,
		Body:   body,
		Group:  "z-pic-watch",
		Silent: true,
	})
	go func() {
		_ = n.Show()
	}()
}

func (s *application) startAutoFolderWatches() {
	rules := s.allWatchRules()
	var firstErr error
	for i, rule := range rules {
		if !rule.AutoStart || rule.InputDir == "" || rule.OutputDir == "" {
			continue
		}
		if s.watchSessionForRule(i) != nil {
			continue
		}
		if err := s.startWatchRuleWithNotify(i, rule, s.watch.settings.Notify, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		s.watch.err = firstErr
		s.invalidateFolderWatchViews()
	}
}

func (s *application) startAllFolderWatches() {
	rules := s.allWatchRules()
	var firstErr error
	for i, rule := range rules {
		if rule.InputDir == "" || rule.OutputDir == "" {
			continue
		}
		if s.watchSessionForRule(i) != nil {
			continue
		}
		if err := s.startWatchRuleWithNotify(i, rule, s.watch.settings.Notify, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		s.watch.err = firstErr
		s.invalidateFolderWatchViews()
	}
}

func (s *application) stopWatchRule(ruleIndex int) {
	if session := s.watchSessionForRule(ruleIndex); session != nil && !session.stopping {
		session.stopping = true
		session.cancel()
		s.invalidateFolderWatchViews()
		s.refreshTrayMenu()
	}
}

func (s *application) updateFolderWatch(session *desktopWatchSession, apply func()) {
	s.updateWithInvalidate(false, func() {
		if s.watchSessionForRule(session.ruleIndex) != session {
			return
		}
		apply()
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
	})
}

func (s *application) stopFolderWatch() {
	s.stopAllFolderWatches()
}

func (s *application) stopAllFolderWatches() {
	changed := false
	for _, session := range s.activeFolderWatchSessions() {
		if !session.stopping {
			session.stopping = true
			session.cancel()
			changed = true
		}
	}
	if changed {
		s.invalidateFolderWatchViews()
		s.refreshTrayMenu()
	}
}

func (s *application) closeFolderWatch() {
	sessions := s.activeFolderWatchSessions()
	for _, session := range sessions {
		session.cancel()
	}
	for _, session := range sessions {
		<-session.done
	}
	s.watch.session = nil
	s.watch.ruleSessions = nil
}

func (s *application) invalidateFolderWatchViews() {
	if s.closed {
		return
	}
	if s.window != nil {
		s.window.Invalidate()
	}
	if s.settingsWindow != nil {
		s.settingsWindow.Invalidate()
	}
}
