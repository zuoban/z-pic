package main

import (
	"context"
	"sync"

	"zpic/internal/compress"
)

const imageMemoryLimit int64 = 512 << 20

type imageMemoryBudget struct {
	mu      sync.Mutex
	limit   int64
	used    int64
	changed chan struct{}
}

func newImageMemoryBudget(limit int64) *imageMemoryBudget {
	return &imageMemoryBudget{limit: max(1, limit), changed: make(chan struct{})}
}

func (budget *imageMemoryBudget) acquire(ctx context.Context, estimate int64) (func(), error) {
	weight := min(budget.limit, max(1, estimate))
	for {
		budget.mu.Lock()
		if err := ctx.Err(); err != nil {
			budget.mu.Unlock()
			return nil, err
		}
		if weight <= budget.limit-budget.used {
			budget.used += weight
			budget.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					budget.mu.Lock()
					budget.used -= weight
					close(budget.changed)
					budget.changed = make(chan struct{})
					budget.mu.Unlock()
				})
			}, nil
		}
		changed := budget.changed
		budget.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *application) acquireImageMemory(ctx context.Context, info compress.ImageInfo, thumbnail bool) (func(), error) {
	if s == nil || s.imageMemory == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	return s.imageMemory.acquire(ctx, imageMemoryEstimate(info, thumbnail))
}

func (s *application) acquirePreviewMemory(ctx context.Context, info compress.ImageInfo) (func(), error) {
	if s == nil || s.imageMemory == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	return s.imageMemory.acquire(ctx, previewMemoryEstimate(info))
}

func previewMemoryEstimate(info compress.ImageInfo) int64 {
	if info.Width <= 0 || info.Height <= 0 ||
		int64(info.Width) > int64(maxPixels)/int64(info.Height) ||
		info.Size < 0 || info.Size > maxInputBytes {
		return imageMemoryLimit
	}
	return int64(info.Width)*int64(info.Height)*12 + 2*info.Size + 4<<20
}

func imageMemoryEstimate(info compress.ImageInfo, thumbnail bool) int64 {
	if info.Width <= 0 || info.Height <= 0 ||
		int64(info.Width) > int64(maxPixels)/int64(info.Height) ||
		info.Size < 0 || info.Size > maxInputBytes {
		return imageMemoryLimit
	}
	if thumbnail {
		return int64(info.Width)*int64(info.Height)*8 + info.Size + 2<<20
	}
	pixelBytes := int64(24)
	if info.Format == "jpeg" {
		pixelBytes = 8
	} else if info.Format == "webp" {
		pixelBytes = 16
	}
	return int64(info.Width)*int64(info.Height)*pixelBytes + 3*info.Size + 8<<20
}
