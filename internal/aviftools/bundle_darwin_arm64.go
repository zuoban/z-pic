//go:build darwin && arm64

package aviftools

import _ "embed"

//go:embed assets/darwin_arm64/avifenc
var avifenc []byte

//go:embed assets/darwin_arm64/avifdec
var avifdec []byte

func bundledTool(name string) []byte {
	switch name {
	case "avifenc":
		return avifenc
	case "avifdec":
		return avifdec
	default:
		return nil
	}
}
