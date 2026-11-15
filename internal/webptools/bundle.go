package webptools

import (
	"embed"
	"runtime"
)

//go:embed assets
var tools embed.FS

func bundledTool() []byte {
	data, _ := tools.ReadFile("assets/" + runtime.GOOS + "_" + runtime.GOARCH + "/cwebp")
	return data
}
