package webptools

import (
	"embed"
	"runtime"
)

//go:embed assets
var tools embed.FS

func bundledTool() []byte {
	name := "cwebp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	data, _ := tools.ReadFile("assets/" + runtime.GOOS + "_" + runtime.GOARCH + "/" + name)
	return data
}
