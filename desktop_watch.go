package main

import (
	"context"
	"errors"
	"fmt"

	"zpic/internal/compress"
)

type folderWatchSettings struct {
	InputDir  string `json:"input_dir,omitempty"`
	OutputDir string `json:"output_dir,omitempty"`
	Preset    string `json:"preset,omitempty"`
}

type desktopFolderWatch struct {
	settings folderWatchSettings
	session  *desktopWatchSession
	last     folderWatchOutcome
	err      error
}

// The UI owns the session fields. The worker only signals done and posts updates.
type desktopWatchSession struct {
	settings folderWatchSettings
	cancel   context.CancelFunc
	done     chan struct{}
	ready    bool
	stopping bool
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
	if s.closed {
		return errors.New("应用正在关闭")
	}
	if s.watch.session != nil {
		return errors.New("请等待当前监听停止")
	}
	if settings.InputDir == "" || settings.OutputDir == "" {
		return errors.New("请选择输入目录和输出目录")
	}
	preset, ok := s.folderWatchPreset(settings.Preset)
	if !ok {
		return errors.New("所选监听预设已不可用，请重新选择")
	}
	settings.Preset = preset.name
	request := folderWatchRequest{
		InputDir: settings.InputDir, OutputDir: settings.OutputDir, Options: preset.compressionOptions(),
		AcquireMemory: func(ctx context.Context, info compress.ImageInfo) (func(), error) {
			return s.acquireImageMemory(ctx, info, false)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &desktopWatchSession{settings: settings, cancel: cancel, done: make(chan struct{})}
	s.watch.session = session
	s.watch.last, s.watch.err = folderWatchOutcome{}, nil

	go func() {
		err := runFolderWatch(ctx, request, folderWatchNotifications{
			Ready: func(folderWatchReady) {
				s.updateFolderWatch(session, func() {
					if session.stopping {
						return
					}
					session.ready = true
					if err := s.rememberFolderWatchSettings(session.settings); err != nil {
						s.watch.err = fmt.Errorf("无法记住监听设置：%w", err)
					}
				})
			},
			Image: func(outcome folderWatchOutcome) {
				s.updateFolderWatch(session, func() { s.watch.last = outcome })
			},
		})
		cancel()
		close(session.done)
		s.updateFolderWatch(session, func() {
			if !session.stopping && err != nil {
				s.watch.err = err
			}
			s.watch.session = nil
		})
	}()
	s.invalidateFolderWatchViews()
	return nil
}

func (s *application) rememberFolderWatchSettings(settings folderWatchSettings) error {
	if s.watch.settings == settings {
		return nil
	}
	if s.settingsError != "" {
		return errors.New("请先在设置中保存以修复设置文件")
	}
	if s.settingsFile != "" {
		if err := s.saveSettingsSnapshot(s.presets, s.scheme, s.batchOutputDir, s.effectiveThemeMode(), s.autoCompress, s.backupToTrash, s.downloadDir, settings); err != nil {
			return err
		}
	}
	s.watch.settings = settings
	return nil
}

func (s *application) updateFolderWatch(session *desktopWatchSession, apply func()) {
	s.updateWithInvalidate(false, func() {
		if s.watch.session != session {
			return
		}
		apply()
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
	})
}

func (s *application) stopFolderWatch() {
	if session := s.watch.session; session != nil && !session.stopping {
		session.stopping = true
		session.cancel()
		s.invalidateFolderWatchViews()
	}
}

func (s *application) closeFolderWatch() {
	if session := s.watch.session; session != nil {
		session.cancel()
		<-session.done
		s.watch.session = nil
	}
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
