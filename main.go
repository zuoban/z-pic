package main

import (
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func newDesktopApplication() *application {
	app := newApplication()
	app.restoreSettings()
	return app
}

func main() {
	if shouldRunCLI(os.Args[1:]) {
		os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
	}
	app := newDesktopApplication()
	mygo.Theme.SetSource(app.effectiveThemeMode().source())
	var pendingOpenFiles []string
	mygo.App.OnOpenFile(func(path string) {
		if path == "" {
			return
		}
		if app.window == nil {
			pendingOpenFiles = append(pendingOpenFiles, path)
			return
		}
		app.update(func() {
			app.showMainWindow()
			app.dropFiles([]string{path})
		})
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if app.window != nil && (!hasVisibleWindows || !app.window.IsVisible()) {
			app.showMainWindow()
			app.refreshTrayMenu()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		app.quitting = true
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		app.quitting = true
		app.cancelUpdateCheck()
		app.clearWindowProgress()
		app.closeFolderWatch()
		app.closeTray()
	})
	mygo.App.WhenReady(func() {
		startup := mergeStartupPaths(os.Args[1:], pendingOpenFiles)
		pendingOpenFiles = nil
		startHidden := shouldStartMainWindowHidden(mygo.App.WasOpenedAtLogin(), app.keepInMenuBar, len(startup))
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Z Pic · 图片压缩",
			Width:           980,
			Height:          720,
			MinWidth:        760,
			MinHeight:       660,
			Hidden:          startHidden,
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  mainTitleBarHeight,
			Vibrancy:        mygo.VibrancyGlass,
			VibrancyTint:    windowVibrancyTintCSS,
			BackgroundColor: windowBackgroundCSS,
			UseContentSize:  true,
			Content:         ui.View(app.view),
		})
		app.window.OnClose(func(e *mygo.CloseEvent) {
			if app.shouldHideMainWindowOnClose() {
				e.PreventDefault()
				app.window.Hide()
				app.refreshTrayMenu()
			}
		})
		app.window.OnShow(func() { app.refreshTrayMenu() })
		app.window.OnHide(func() { app.refreshTrayMenu() })
		app.window.OnClosed(func() {
			app.closed = true
			app.stopFolderWatch()
			app.cancelComparisonDiff()
			app.cancelUpdateCheck()
			app.clearWindowProgress()
			if app.cancel != nil {
				app.cancel()
			}
			if app.thumbnails.cancel != nil {
				app.thumbnails.cancel()
			}
			app.dropQueue = nil
			if app.dropTransport != nil {
				app.dropTransport.CloseIdleConnections()
			}
			app.setFloatingOpen(false)
			app.closeTray()
		})
		app.window.OnFileDrop(func(e *mygo.FileDropEvent) {
			app.dropFiles(e.Paths)
		})
		app.setupTray()
		if startHidden && app.tray == nil {
			app.window.Show()
		}
		app.startAutoFolderWatches()
		if len(startup) > 0 {
			app.loadStartupPaths(startup)
		}
	})
	err := mygo.App.Run()
	app.closeFolderWatch()
	app.closeTray()
	app.saves.Wait()
	app.cleanupDragStaging()
	if err != nil {
		log.Fatal(err)
	}
}

func mergeStartupPaths(args, opened []string) []string {
	if len(args) == 0 && len(opened) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(args)+len(opened))
	merged := make([]string, 0, len(args)+len(opened))
	for listIdx, list := range [][]string{args, opened} {
		for _, path := range list {
			if path == "" || seen[path] {
				continue
			}
			if listIdx == 0 && strings.HasPrefix(path, "-psn_") {
				continue
			}
			seen[path] = true
			merged = append(merged, path)
		}
	}
	return merged
}

func shouldStartMainWindowHidden(openedAtLogin, keepInMenuBar bool, startupPathCount int) bool {
	return openedAtLogin && keepInMenuBar && startupPathCount == 0
}
