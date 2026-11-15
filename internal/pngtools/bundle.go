package pngtools

import (
	"embed"
	"runtime"
)

//go:embed assets
var tools embed.FS

func bundledTool(name string) []byte {
	if name != "oxipng" && name != "pngquant" {
		return nil
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	data, _ := tools.ReadFile("assets/" + runtime.GOOS + "_" + runtime.GOARCH + "/" + name)
	return data
}
