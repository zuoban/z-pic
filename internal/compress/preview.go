package compress

import (
	"bytes"
	"context"
	"fmt"
	"image"
)

// BytesWithPreview follows Bytes's contract and also returns decoded output
// pixels for a changed result, reusing the PNG/WebP validation decode.
// An unchanged result returns a nil preview so callers can reuse the source.
func BytesWithPreview(ctx context.Context, data []byte, sourcePath string, options Options) (*Result, image.Image, error) {
	absolutePath, err := validateRequest(ctx, sourcePath, options)
	if err != nil {
		return nil, nil, err
	}
	var preview image.Image
	result, err := compressData(ctx, data, sourcePath, absolutePath, options, &preview)
	if err != nil {
		return nil, nil, err
	}
	if !result.Unchanged && preview == nil {
		preview, _, err = image.Decode(contextReader{ctx: ctx, reader: bytes.NewReader(result.Data)})
		if err != nil {
			return nil, nil, fmt.Errorf("无法生成预览：%w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if result.Unchanged {
		return result, nil, nil
	}
	return result, preview, nil
}
