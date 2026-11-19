//go:build !(darwin && arm64)

package webptools

func bundledTool() []byte { return nil }
