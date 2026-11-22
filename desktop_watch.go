package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

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

const watchPrimaryRuleID uint64 = 0

type desktopFolderWatch struct {
	settings     folderWatchSettings
	extraRules   []folderWatchRule
	extraRuleIDs []uint64
	nextRuleID   uint64
	session      *desktopWatchSession
	ruleSessions map[uint64]*desktopWatchSession
	last         folderWatchOutcome
	err          error
}

// The UI owns the session fields. The worker only signals done and posts updates.
type desktopWatchSession struct {
	ruleID   uint64
	settings folderWatchSettings
	rule     folderWatchRule
	cancel   context.CancelFunc
	done     chan struct{}
	ready    bool
	stopping bool
	last     folderWatchOutcome
	err      error
}

type identifiedWatchRule struct {
	id   uint64
	rule folderWatchRule
}

func (s *application) allocWatchRuleID() uint64 {
	for {
		s.watch.nextRuleID++
		id := s.watch.nextRuleID
		if id == watchPrimaryRuleID {
			continue
		}
		if s.watch.ruleSessions != nil && s.watch.ruleSessions[id] != nil {
			continue
		}
		if slices.Contains(s.watch.extraRuleIDs, id) {
			continue
		}
		if s.editor != nil && slices.Contains(s.editor.watchRuleIDs, id) {
			continue
		}
		return id
	}
}

func (s *application) ensureWatchRuleIDs() {
	for len(s.watch.extraRuleIDs) < len(s.watch.extraRules) {
		s.watch.extraRuleIDs = append(s.watch.extraRuleIDs, s.allocWatchRuleID())
	}
	for i, id := range s.watch.extraRuleIDs {
		if id == watchPrimaryRuleID {
			s.watch.extraRuleIDs[i] = s.allocWatchRuleID()
		}
	}
}

func (s *application) ensureEditorWatchRuleIDs(editor *settingsEditor) {
	if editor == nil {
		return
	}
	for len(editor.watchRuleIDs) < len(editor.watchRules) {
		editor.watchRuleIDs = append(editor.watchRuleIDs, s.allocWatchRuleID())
	}
	if len(editor.watchRuleIDs) > len(editor.watchRules) {
		editor.watchRuleIDs = editor.watchRuleIDs[:len(editor.watchRules)]
	}
	for i, id := range editor.watchRuleIDs {
		if id == watchPrimaryRuleID {
			editor.watchRuleIDs[i] = s.allocWatchRuleID()
		}
	}
}

func (s *application) ensureWatchRuleSlotID(ruleIndex int) uint64 {
	if ruleIndex <= 0 {
		return watchPrimaryRuleID
	}
	s.ensureWatchRuleIDs()
	extraIdx := ruleIndex - 1
	for len(s.watch.extraRuleIDs) <= extraIdx {
		s.watch.extraRuleIDs = append(s.watch.extraRuleIDs, s.allocWatchRuleID())
	}
	if s.watch.extraRuleIDs[extraIdx] == watchPrimaryRuleID {
		s.watch.extraRuleIDs[extraIdx] = s.allocWatchRuleID()
	}
	return s.watch.extraRuleIDs[extraIdx]
}

func (s *application) watchRuleIDForIndex(ruleIndex int) (uint64, bool) {
	if ruleIndex == 0 {
		return watchPrimaryRuleID, true
	}
	if ruleIndex < 0 {
		return 0, false
	}
	s.ensureWatchRuleIDs()
	extraIdx := ruleIndex - 1
	if extraIdx >= len(s.watch.extraRuleIDs) {
		return 0, false
	}
	id := s.watch.extraRuleIDs[extraIdx]
	if id == watchPrimaryRuleID {
		return 0, false
	}
	return id, true
}

func (s *application) allWatchRules() []folderWatchRule {
	rules := make([]folderWatchRule, 0, 1+len(s.watch.extraRules))
	rules = append(rules, s.watch.settings.primaryRule())
	rules = append(rules, s.watch.extraRules...)
	return rules
}

func (s *application) watchRulesWithIDs() []identifiedWatchRule {
	s.ensureWatchRuleIDs()
	rules := make([]identifiedWatchRule, 0, 1+len(s.watch.extraRules)+len(s.watch.ruleSessions))
	primary := s.watch.settings.primaryRule()
	if s.watch.session != nil && primary.InputDir == "" && primary.OutputDir == "" {
		primary = s.watch.session.rule
	}
	rules = append(rules, identifiedWatchRule{
		id:   watchPrimaryRuleID,
		rule: primary,
	})
	seen := make(map[uint64]bool, len(s.watch.extraRules))
	for i, rule := range s.watch.extraRules {
		id := s.watch.extraRuleIDs[i]
		seen[id] = true
		rules = append(rules, identifiedWatchRule{
			id:   id,
			rule: rule,
		})
	}
	if len(s.watch.ruleSessions) > 0 {
		extraIDs := make([]uint64, 0, len(s.watch.ruleSessions))
		for id, sess := range s.watch.ruleSessions {
			if sess != nil && !seen[id] {
				extraIDs = append(extraIDs, id)
			}
		}
		slices.Sort(extraIDs)
		for _, id := range extraIDs {
			rules = append(rules, identifiedWatchRule{
				id:   id,
				rule: s.watch.ruleSessions[id].rule,
			})
		}
	}
	return rules
}

func (s *application) watchSessionForRuleID(ruleID uint64) *desktopWatchSession {
	if ruleID == watchPrimaryRuleID {
		return s.watch.session
	}
	if s.watch.ruleSessions == nil {
		return nil
	}
	return s.watch.ruleSessions[ruleID]
}

func (s *application) setWatchSessionForRuleID(ruleID uint64, session *desktopWatchSession) {
	if ruleID == watchPrimaryRuleID {
		if session != nil {
			session.ruleID = watchPrimaryRuleID
		}
		s.watch.session = session
		return
	}
	if session == nil {
		delete(s.watch.ruleSessions, ruleID)
		return
	}
	session.ruleID = ruleID
	if s.watch.ruleSessions == nil {
		s.watch.ruleSessions = make(map[uint64]*desktopWatchSession)
	}
	s.watch.ruleSessions[ruleID] = session
}

func (s *application) watchSessionForRule(ruleIndex int) *desktopWatchSession {
	ruleID, ok := s.watchRuleIDForIndex(ruleIndex)
	if !ok {
		return nil
	}
	return s.watchSessionForRuleID(ruleID)
}

func (s *application) setWatchSessionForRule(ruleIndex int, session *desktopWatchSession) {
	if ruleIndex == 0 {
		s.setWatchSessionForRuleID(watchPrimaryRuleID, session)
		return
	}
	if session == nil {
		if ruleID, ok := s.watchRuleIDForIndex(ruleIndex); ok {
			s.setWatchSessionForRuleID(ruleID, nil)
		}
		return
	}
	ruleID := s.ensureWatchRuleSlotID(ruleIndex)
	s.setWatchSessionForRuleID(ruleID, session)
}

func (s *application) activeFolderWatchSessions() []*desktopWatchSession {
	var list []*desktopWatchSession
	if s.watch.session != nil {
		list = append(list, s.watch.session)
	}
	if len(s.watch.ruleSessions) > 0 {
		keys := make([]uint64, 0, len(s.watch.ruleSessions))
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

type watchPathInfo struct {
	resolved     string
	stat         os.FileInfo
	existingStat os.FileInfo
	tail         []string
}

type watchPathRel int

const (
	watchPathDisjoint watchPathRel = iota
	watchPathSame
	watchPathAncestor
	watchPathDescendant
)

func inspectWatchPath(raw, roleLabel string) (watchPathInfo, error) {
	if raw == "" {
		return watchPathInfo{}, fmt.Errorf("请选择%s", roleLabel)
	}
	if info, err := os.Stat(raw); err == nil && !info.IsDir() {
		return watchPathInfo{}, fmt.Errorf("%s不是文件夹", roleLabel)
	}
	resolved, err := resolveOutputDirectory(context.Background(), raw)
	if err != nil {
		return watchPathInfo{}, fmt.Errorf("%s无效：%w", roleLabel, err)
	}
	resolved = filepath.Clean(resolved)
	var stat os.FileInfo
	if info, err := os.Stat(resolved); err == nil {
		stat = info
	}
	var existingStat os.FileInfo
	var revTail []string
	for cur := resolved; ; {
		if info, err := os.Stat(cur); err == nil {
			existingStat = info
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		revTail = append(revTail, filepath.Base(cur))
		cur = parent
	}
	slices.Reverse(revTail)
	return watchPathInfo{
		resolved:     resolved,
		stat:         stat,
		existingStat: existingStat,
		tail:         revTail,
	}, nil
}

func lexicalWatchPathRelation(pathA, pathB string) (watchPathRel, bool) {
	if rel, err := filepath.Rel(pathA, pathB); err == nil {
		if rel == "." {
			return watchPathSame, true
		}
		if filepath.IsLocal(rel) {
			return watchPathAncestor, true
		}
	}
	if rel, err := filepath.Rel(pathB, pathA); err == nil {
		if rel == "." {
			return watchPathSame, true
		}
		if filepath.IsLocal(rel) {
			return watchPathDescendant, true
		}
	}
	return watchPathDisjoint, false
}

func ancestorSameFileRelation(targetStat os.FileInfo, path string) (watchPathRel, bool) {
	for depth, cur := 0, path; ; depth++ {
		if info, err := os.Stat(cur); err == nil && os.SameFile(targetStat, info) {
			if depth == 0 {
				return watchPathSame, true
			}
			return watchPathAncestor, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return watchPathDisjoint, false
		}
		cur = parent
	}
}

func watchPathRelation(pathA, pathB watchPathInfo) watchPathRel {
	if pathA.stat != nil && pathB.stat != nil && os.SameFile(pathA.stat, pathB.stat) {
		return watchPathSame
	}
	if rel, ok := lexicalWatchPathRelation(pathA.resolved, pathB.resolved); ok {
		return rel
	}
	if pathA.stat != nil {
		if rel, ok := ancestorSameFileRelation(pathA.stat, pathB.resolved); ok {
			return rel
		}
	}
	if pathB.stat != nil {
		if rel, ok := ancestorSameFileRelation(pathB.stat, pathA.resolved); ok {
			switch rel {
			case watchPathSame:
				return watchPathSame
			case watchPathAncestor:
				return watchPathDescendant
			}
		}
	}
	if pathA.existingStat != nil && pathB.existingStat != nil && os.SameFile(pathA.existingStat, pathB.existingStat) {
		if watchPathSegmentsEqual(pathA.tail, pathB.tail) {
			return watchPathSame
		}
		if len(pathA.tail) < len(pathB.tail) && watchPathSegmentsEqual(pathA.tail, pathB.tail[:len(pathA.tail)]) {
			return watchPathAncestor
		}
		if len(pathB.tail) < len(pathA.tail) && watchPathSegmentsEqual(pathB.tail, pathA.tail[:len(pathB.tail)]) {
			return watchPathDescendant
		}
	}
	return watchPathDisjoint
}

func watchPathSegmentsEqual(a, b []string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return slices.EqualFunc(a, b, strings.EqualFold)
	}
	return slices.Equal(a, b)
}

func watchDirsOverlap(pathA watchPathInfo, recA bool, pathB watchPathInfo, recB bool) bool {
	switch watchPathRelation(pathA, pathB) {
	case watchPathSame:
		return true
	case watchPathAncestor:
		return recA
	case watchPathDescendant:
		return recB
	default:
		return false
	}
}

func watchOutputFeedsInput(outPath watchPathInfo, outRecursive bool, inPath watchPathInfo, inRecursive bool) bool {
	switch watchPathRelation(outPath, inPath) {
	case watchPathSame:
		return true
	case watchPathDescendant:
		return inRecursive
	case watchPathAncestor:
		return outRecursive
	default:
		return false
	}
}

func validateSingleWatchRule(rule folderWatchRule) (watchPathInfo, watchPathInfo, error) {
	inInfo, err := inspectWatchPath(rule.InputDir, "输入目录")
	if err != nil {
		return watchPathInfo{}, watchPathInfo{}, err
	}
	outInfo, err := inspectWatchPath(rule.OutputDir, "输出目录")
	if err != nil {
		return watchPathInfo{}, watchPathInfo{}, err
	}
	switch watchPathRelation(inInfo, outInfo) {
	case watchPathSame, watchPathAncestor:
		return watchPathInfo{}, watchPathInfo{}, errors.New("输出目录必须位于输入目录之外")
	case watchPathDescendant:
		if rule.Recursive {
			return watchPathInfo{}, watchPathInfo{}, errors.New("开启包含子目录时，输入目录不能位于输出目录之内")
		}
	}
	return inInfo, outInfo, nil
}

func validateWatchRulePair(labelA string, ruleA folderWatchRule, inA, outA watchPathInfo, labelB string, ruleB folderWatchRule, inB, outB watchPathInfo) error {
	aFeedsB := watchOutputFeedsInput(outA, ruleA.Recursive, inB, ruleB.Recursive)
	bFeedsA := watchOutputFeedsInput(outB, ruleB.Recursive, inA, ruleA.Recursive)
	if aFeedsB && bFeedsA {
		return fmt.Errorf("%s与%s的输出目录与输入目录互为循环，请调整目录", labelA, labelB)
	}
	if aFeedsB {
		return fmt.Errorf("%s的输出目录与%s的输入目录冲突，可能触发重复压缩或循环", labelA, labelB)
	}
	if bFeedsA {
		return fmt.Errorf("%s的输出目录与%s的输入目录冲突，可能触发重复压缩或循环", labelB, labelA)
	}
	if watchDirsOverlap(inA, ruleA.Recursive, inB, ruleB.Recursive) && watchDirsOverlap(outA, ruleA.Recursive, outB, ruleB.Recursive) {
		return fmt.Errorf("%s与%s的输入目录和输出目录重复或重叠", labelA, labelB)
	}
	return nil
}

func (s *application) watchRuleLabelForID(ruleID uint64) string {
	if ruleID == watchPrimaryRuleID {
		return "主监听规则"
	}
	if s.editor != nil {
		if idx := slices.Index(s.editor.watchRuleIDs, ruleID); idx >= 0 {
			return fmt.Sprintf("监听规则 %d", idx+2)
		}
	}
	if idx := slices.Index(s.watch.extraRuleIDs, ruleID); idx >= 0 {
		return fmt.Sprintf("监听规则 %d", idx+2)
	}
	return "其他监听规则"
}

func (s *application) validateFolderWatchRules(primary folderWatchRule, extraRules []folderWatchRule, extraRuleIDs []uint64) error {
	type candidate struct {
		id    uint64
		label string
		rule  folderWatchRule
		in    watchPathInfo
		out   watchPathInfo
	}
	var candidates []candidate
	seenIDs := make(map[uint64]bool, 1+len(extraRules))
	appendRule := func(id uint64, label string, rule folderWatchRule) error {
		seenIDs[id] = true
		if rule.InputDir == "" || rule.OutputDir == "" {
			return nil
		}
		inInfo, outInfo, err := validateSingleWatchRule(rule)
		if err != nil {
			return fmt.Errorf("%s：%w", label, err)
		}
		candidates = append(candidates, candidate{
			id:    id,
			label: label,
			rule:  rule,
			in:    inInfo,
			out:   outInfo,
		})
		return nil
	}
	if err := appendRule(watchPrimaryRuleID, "主监听规则", primary); err != nil {
		return err
	}
	for i, rule := range extraRules {
		var id uint64
		if i < len(extraRuleIDs) {
			id = extraRuleIDs[i]
		}
		if err := appendRule(id, fmt.Sprintf("监听规则 %d", i+2), rule); err != nil {
			return err
		}
	}
	for _, sess := range s.activeFolderWatchSessions() {
		if seenIDs[sess.ruleID] {
			continue
		}
		if err := appendRule(sess.ruleID, s.watchRuleLabelForID(sess.ruleID), sess.rule); err != nil {
			return err
		}
	}
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if err := validateWatchRulePair(
				candidates[i].label, candidates[i].rule, candidates[i].in, candidates[i].out,
				candidates[j].label, candidates[j].rule, candidates[j].in, candidates[j].out,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *application) validateWatchRulePeers(ruleID uint64, rule folderWatchRule, includeSaved bool, nextWatch folderWatchSettings, nextExtra []folderWatchRule, nextExtraIDs []uint64) error {
	inInfo, outInfo, err := validateSingleWatchRule(rule)
	if err != nil {
		return err
	}
	label := s.watchRuleLabelForID(ruleID)
	checkPeer := func(otherID uint64, otherLabel string, otherRule folderWatchRule) error {
		if otherID == ruleID {
			return nil
		}
		if otherRule.InputDir == "" || otherRule.OutputDir == "" {
			return nil
		}
		otherIn, otherOut, err := validateSingleWatchRule(otherRule)
		if err != nil {
			return nil
		}
		return validateWatchRulePair(label, rule, inInfo, outInfo, otherLabel, otherRule, otherIn, otherOut)
	}
	for _, sess := range s.activeFolderWatchSessions() {
		if err := checkPeer(sess.ruleID, s.watchRuleLabelForID(sess.ruleID), sess.rule); err != nil {
			return err
		}
	}
	if !includeSaved {
		return nil
	}
	if err := checkPeer(watchPrimaryRuleID, "主监听规则", nextWatch.primaryRule()); err != nil {
		return err
	}
	for i, otherRule := range nextExtra {
		var otherID uint64
		if i < len(nextExtraIDs) {
			otherID = nextExtraIDs[i]
		}
		otherLabel := fmt.Sprintf("监听规则 %d", i+2)
		if otherID != 0 {
			otherLabel = s.watchRuleLabelForID(otherID)
		}
		if err := checkPeer(otherID, otherLabel, otherRule); err != nil {
			return err
		}
	}
	return nil
}

func (s *application) validateWatchRuleStart(ruleID uint64, rule folderWatchRule, persist bool) error {
	if !persist {
		return s.validateWatchRulePeers(ruleID, rule, false, folderWatchSettings{}, nil, nil)
	}
	nextWatch, nextExtra, nextExtraIDs := s.nextPersistedWatchState(ruleID, rule, s.watch.settings.Notify)
	return s.validateWatchRulePeers(ruleID, rule, true, nextWatch, nextExtra, nextExtraIDs)
}

func (s *application) startFolderWatch(settings folderWatchSettings) error {
	return s.startWatchRuleByID(watchPrimaryRuleID, settings.primaryRule(), settings.Notify, true)
}

func (s *application) startWatchRule(ruleIndex int, rule folderWatchRule) error {
	return s.startWatchRuleWithNotify(ruleIndex, rule, s.watch.settings.Notify, true)
}

func (s *application) startWatchRuleWithNotify(ruleIndex int, rule folderWatchRule, notifyEnabled bool, persist bool) error {
	ruleID := s.ensureWatchRuleSlotID(ruleIndex)
	return s.startWatchRuleByID(ruleID, rule, notifyEnabled, persist)
}

func (s *application) startWatchRuleByID(ruleID uint64, rule folderWatchRule, notifyEnabled bool, persist bool) error {
	if s.closed {
		return errors.New("应用正在关闭")
	}
	if s.watchSessionForRuleID(ruleID) != nil {
		return errors.New("请等待当前监听停止")
	}
	if rule.InputDir == "" || rule.OutputDir == "" {
		return errors.New("请选择输入目录和输出目录")
	}
	preset, ok := s.folderWatchPreset(rule.Preset)
	if !ok {
		return errors.New("所选监听预设已不可用，请重新选择")
	}
	if err := s.validateWatchRuleStart(ruleID, rule, persist); err != nil {
		return err
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
		ruleID:   ruleID,
		settings: settings,
		rule:     rule,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	s.setWatchSessionForRuleID(ruleID, session)
	if ruleID == watchPrimaryRuleID {
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
						if err := s.rememberFolderWatchRuleByID(ruleID, rule, notifyEnabled); err != nil {
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
			s.setWatchSessionForRuleID(ruleID, nil)
			s.refreshTrayMenu()
		})
	}()
	s.invalidateFolderWatchViews()
	s.refreshTrayMenu()
	return nil
}

func (s *application) rememberFolderWatchSettings(settings folderWatchSettings) error {
	return s.rememberFolderWatchRuleByID(watchPrimaryRuleID, settings.primaryRule(), settings.Notify)
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
	ruleID := s.ensureWatchRuleSlotID(ruleIndex)
	return s.rememberFolderWatchRuleByID(ruleID, rule, notifyEnabled)
}

func (s *application) nextPersistedWatchState(ruleID uint64, rule folderWatchRule, notifyEnabled bool) (folderWatchSettings, []folderWatchRule, []uint64) {
	s.ensureWatchRuleIDs()
	nextWatch := s.watch.settings
	nextWatch.Notify = notifyEnabled
	nextExtra := slices.Clone(s.watch.extraRules)
	nextExtraIDs := slices.Clone(s.watch.extraRuleIDs)
	if ruleID == watchPrimaryRuleID {
		nextWatch.applyPrimaryRule(rule)
	} else {
		extraIdx := slices.Index(nextExtraIDs, ruleID)
		if extraIdx >= 0 {
			for len(nextExtra) <= extraIdx {
				nextExtra = append(nextExtra, folderWatchRule{})
			}
			nextExtra[extraIdx] = rule
		} else {
			if len(nextExtraIDs) > len(nextExtra) {
				nextExtraIDs = nextExtraIDs[:len(nextExtra)]
			}
			nextExtra = append(nextExtra, rule)
			nextExtraIDs = append(nextExtraIDs, ruleID)
		}
	}
	return nextWatch, nextExtra, nextExtraIDs
}

func (s *application) rememberFolderWatchRuleByID(ruleID uint64, rule folderWatchRule, notifyEnabled bool) error {
	nextWatch, nextExtra, nextExtraIDs := s.nextPersistedWatchState(ruleID, rule, notifyEnabled)
	if nextWatch == s.watch.settings && slices.Equal(nextExtra, s.watch.extraRules) && slices.Equal(nextExtraIDs, s.watch.extraRuleIDs) {
		return nil
	}
	if err := s.validateWatchRulePeers(ruleID, rule, true, nextWatch, nextExtra, nextExtraIDs); err != nil {
		return err
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
	s.watch.extraRuleIDs = nextExtraIDs
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
	rules := s.watchRulesWithIDs()
	var firstErr error
	for _, item := range rules {
		rule := item.rule
		if !rule.AutoStart || rule.InputDir == "" || rule.OutputDir == "" {
			continue
		}
		if s.watchSessionForRuleID(item.id) != nil {
			continue
		}
		if err := s.startWatchRuleByID(item.id, rule, s.watch.settings.Notify, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		s.watch.err = firstErr
		s.invalidateFolderWatchViews()
	}
}

func (s *application) startAllFolderWatches() {
	rules := s.watchRulesWithIDs()
	var firstErr error
	for _, item := range rules {
		rule := item.rule
		if rule.InputDir == "" || rule.OutputDir == "" {
			continue
		}
		if s.watchSessionForRuleID(item.id) != nil {
			continue
		}
		if err := s.startWatchRuleByID(item.id, rule, s.watch.settings.Notify, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		s.watch.err = firstErr
		s.invalidateFolderWatchViews()
	}
}

func (s *application) stopWatchRule(ruleIndex int) {
	if ruleIndex == 0 {
		s.stopWatchRuleByID(watchPrimaryRuleID)
		return
	}
	if ruleID, ok := s.watchRuleIDForIndex(ruleIndex); ok {
		s.stopWatchRuleByID(ruleID)
	}
}

func (s *application) stopWatchRuleByID(ruleID uint64) {
	if session := s.watchSessionForRuleID(ruleID); session != nil && !session.stopping {
		session.stopping = true
		session.cancel()
		s.invalidateFolderWatchViews()
		s.refreshTrayMenu()
	}
}

func (s *application) updateFolderWatch(session *desktopWatchSession, apply func()) {
	s.updateWithInvalidate(false, func() {
		if s.watchSessionForRuleID(session.ruleID) != session {
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
