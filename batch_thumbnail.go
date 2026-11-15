package main

import (
	"context"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const (
	batchThumbnailPixels = 144
	maxBatchThumbnails   = 96
)

type batchThumbnail struct {
	bitmap *ui.Bitmap
	failed bool
	used   uint64
}

type batchThumbnailCache struct {
	owner          *batchState
	entries        map[string]*batchThumbnail
	wanted         []string
	floatingWanted []string
	floatingNext   bool
	clock          uint64
	active         *batchThumbnail
	cancel         context.CancelFunc
}

func (s *application) prepareBatchThumbnails() {
	s.prepareThumbnailCache()
	s.thumbnails.wanted = s.thumbnails.wanted[:0]
}

func (s *application) prepareFloatingThumbnails() {
	s.prepareThumbnailCache()
	s.thumbnails.floatingWanted = s.thumbnails.floatingWanted[:0]
}

func (s *application) prepareThumbnailCache() {
	cache := &s.thumbnails
	if cache.owner != s.batch {
		if cache.cancel != nil {
			cache.cancel()
		}
		cache.owner = s.batch
		cache.entries = nil
		cache.wanted = nil
		cache.floatingWanted = nil
		cache.floatingNext = false
	}
}

func (s *application) batchThumbnail(item batchItem) *batchThumbnail {
	return s.requestBatchThumbnail(item, &s.thumbnails.wanted)
}

func (s *application) floatingThumbnail(item batchItem) *batchThumbnail {
	return s.requestBatchThumbnail(item, &s.thumbnails.floatingWanted)
}

func (s *application) requestBatchThumbnail(item batchItem, wanted *[]string) *batchThumbnail {
	if item.invalidInput || item.path == "" {
		return nil
	}
	cache := &s.thumbnails
	key := item.path
	if cache.entries == nil {
		cache.entries = make(map[string]*batchThumbnail)
	}
	entry := cache.entries[key]
	if entry == nil {
		entry = &batchThumbnail{}
		cache.entries[key] = entry
	}
	cache.clock++
	entry.used = cache.clock
	*wanted = append(*wanted, key)
	for len(cache.entries) > maxBatchThumbnails {
		var oldestKey string
		var oldest *batchThumbnail
		for candidateKey, candidate := range cache.entries {
			if candidate == cache.active || candidate == entry {
				continue
			}
			if oldest == nil || candidate.used < oldest.used {
				oldestKey, oldest = candidateKey, candidate
			}
		}
		delete(cache.entries, oldestKey)
	}
	return entry
}

func (s *application) startNextBatchThumbnail() {
	cache := &s.thumbnails
	if s.closed || s.batch == nil || cache.owner != s.batch || cache.active != nil ||
		(s.window == nil && s.dispatchUpdate == nil) {
		return
	}
	demands := [2][]string{cache.wanted, cache.floatingWanted}
	for attempt := range demands {
		surface := attempt
		if cache.floatingNext {
			surface = 1 - attempt
		}
		for _, path := range demands[surface] {
			entry := cache.entries[path]
			if entry == nil || entry.bitmap != nil || entry.failed {
				continue
			}
			owner := s.batch
			ctx, cancel := context.WithCancel(context.Background())
			cache.active, cache.cancel = entry, cancel
			cache.floatingNext = surface == 0
			go func() {
				thumbnail, err := compress.Thumbnail(ctx, path, batchThumbnailPixels)
				s.update(func() {
					cache.active, cache.cancel = nil, nil
					if s.batch == owner && cache.owner == owner && cache.entries[path] == entry && ctx.Err() == nil {
						entry.failed = err != nil
						if err == nil {
							entry.bitmap = ui.NewBitmap(thumbnail)
						}
					}
					cancel()
					s.startNextBatchThumbnail()
				})
			}()
			return
		}
	}
}

func (s *application) invalidateBatchThumbnail(path string) {
	cache := &s.thumbnails
	if entry := cache.entries[path]; entry != nil {
		if entry == cache.active && cache.cancel != nil {
			cache.cancel()
		}
		delete(cache.entries, path)
	}
}
