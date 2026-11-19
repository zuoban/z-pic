//go:build !darwin

package heictools

import "context"

// Available reports whether system HEIC decoding is available on this platform.
func Available() bool {
	return false
}

// DecodePNG is only available on macOS.
func DecodePNG(ctx context.Context, _ []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupportedPlatform
}

// DecodeThumbnailPNG is only available on macOS.
func DecodeThumbnailPNG(ctx context.Context, _ []byte, _ int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupportedPlatform
}
