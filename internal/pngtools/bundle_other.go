//go:build !(darwin && arm64)

package pngtools

func bundledTool(name string) []byte { return nil }
