//go:build !(darwin && arm64)

package jpegtools

func bundledTool(name string) []byte { return nil }
