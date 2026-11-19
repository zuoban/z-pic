//go:build darwin && arm64

package webptools

import "embed"

//go:embed assets/darwin_arm64
var tools embed.FS

func bundledTool() []byte {
	data, _ := tools.ReadFile("assets/darwin_arm64/cwebp")
	return data
}
