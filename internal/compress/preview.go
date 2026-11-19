package compress

import (
	"bytes"
	"context"
	"fmt"
	"image"

	"zpic/internal/contextio"
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
		preview, _, err = image.Decode(contextio.NewReader(ctx, bytes.NewReader(result.Data)))
		if err != nil {
			return nil, nil, fmt.Errorf("无法生成预览：%w", err)
		}
		if result.Format == "webp" {
			preview = normalizeOrientation(preview, webpOrientation(result.Data))
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
