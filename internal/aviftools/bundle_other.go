//go:build !(darwin && arm64)

package aviftools

func bundledTool(string) []byte { return nil }
