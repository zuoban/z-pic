//go:build darwin && arm64

package jpegtools

import "embed"

//go:embed assets/darwin_arm64
var tools embed.FS

func bundledTool(name string) []byte {
	if name != "cjpeg" && name != "jpegtran" {
		return nil
	}
	data, _ := tools.ReadFile("assets/darwin_arm64/" + name)
	return data
}
