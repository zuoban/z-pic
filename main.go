package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	app := newApplication()
	app.restoreSettings()
	mygo.App.WhenReady(func() {
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title:          "Z Pic · 图片压缩",
			Width:          980,
			Height:         720,
			MinWidth:       880,
			MinHeight:      660,
			UseContentSize: true,
			Content:        ui.View(app.view),
		})
		app.window.OnClosed(func() {
			app.closed = true
			if app.cancel != nil {
				app.cancel()
			}
			if app.thumbnails.cancel != nil {
				app.thumbnails.cancel()
			}
			app.dropQueue = nil
			app.setFloatingOpen(false)
		})
		app.window.OnFileDrop(func(e *mygo.FileDropEvent) {
			app.dropFiles(e.Paths)
		})
		if len(os.Args) > 1 {
			app.loadStartupPaths(os.Args[1:])
		}
	})
	err := mygo.App.Run()
	app.saves.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
