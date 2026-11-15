package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	app := newApplication()
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
		})
		app.window.OnFileDrop(func(e *mygo.FileDropEvent) {
			if app.busy {
				return
			}
			if len(e.Paths) != 1 {
				app.setMessage("请一次选择一张图片。", true)
				return
			}
			app.load(e.Paths[0])
		})
		if len(os.Args) == 2 {
			app.load(os.Args[1])
		}
	})
	err := mygo.App.Run()
	app.saves.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
