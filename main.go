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
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { app.closeFolderWatch() })
	mygo.App.WhenReady(func() {
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Z Pic · 图片压缩",
			Width:           980,
			Height:          720,
			MinWidth:        760,
			MinHeight:       660,
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  mainTitleBarHeight,
			Vibrancy:        mygo.VibrancyGlass,
			VibrancyTint:    windowVibrancyTintCSS,
			BackgroundColor: windowBackgroundCSS,
			UseContentSize:  true,
			Content:         ui.View(app.view),
		})
		app.window.OnClosed(func() {
			app.closed = true
			app.stopFolderWatch()
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
		})
		app.window.OnFileDrop(func(e *mygo.FileDropEvent) {
			app.dropFiles(e.Paths)
		})
		startup := mergeStartupPaths(os.Args[1:], pendingOpenFiles)
		pendingOpenFiles = nil
		if len(startup) > 0 {
			app.loadStartupPaths(startup)
		}
	})
	err := mygo.App.Run()
	app.closeFolderWatch()
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
