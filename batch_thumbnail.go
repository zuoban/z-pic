package main

import (
	"context"
	"image"
	"os"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const (
	batchThumbnailPixels = 144
	maxBatchThumbnails   = 96
	thumbnailRecheck     = 2 * time.Second
)

type batchThumbnail struct {
	bitmap  *ui.Bitmap
	failed  bool
	used    uint64
	info    os.FileInfo
	checked time.Time
}

type batchThumbnailCache struct {
	owner          *batchState
	entries        map[string]*batchThumbnail
	wanted         []string
	floatingWanted []string
	floatingNext   bool
	clock          uint64
	active         *batchThumbnail
	activePath     string
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
	if cache.active != nil {
		if !slices.Contains(cache.wanted, cache.activePath) && !slices.Contains(cache.floatingWanted, cache.activePath) &&
			cache.cancel != nil {
			cache.cancel()
		}
		return
	}
	if s.closed || s.batch == nil || cache.owner != s.batch ||
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
			if entry == nil || (!entry.checked.IsZero() && time.Since(entry.checked) < thumbnailRecheck) {
				continue
			}
			owner := s.batch
			previousInfo, previousBitmap, previousFailed := entry.info, entry.bitmap, entry.failed
			ctx, cancel := context.WithCancel(context.Background())
			cache.active, cache.cancel = entry, cancel
			cache.activePath = path
			cache.floatingNext = surface == 0
			go func() {
				info, statErr := os.Stat(path)
				reused := sameThumbnailFile(previousInfo, info) && (previousBitmap != nil || previousFailed)
				var thumbnail image.Image
				err := statErr
				if err == nil && !reused {
					thumbnail, err = s.loadBatchThumbnail(ctx, path)
				}
				s.updateWithInvalidate(!reused, func() {
					cache.active, cache.cancel = nil, nil
					cache.activePath = ""
					if s.batch == owner && cache.owner == owner && cache.entries[path] == entry && ctx.Err() == nil {
						entry.info, entry.checked = info, time.Now()
						if !reused {
							entry.failed = err != nil
							entry.bitmap = nil
							if err == nil {
								entry.bitmap = ui.NewBitmap(thumbnail)
							}
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

func sameThumbnailFile(previous, current os.FileInfo) bool {
	return previous != nil && current != nil && os.SameFile(previous, current) &&
		previous.Size() == current.Size() && previous.ModTime().Equal(current.ModTime()) &&
		previous.Mode() == current.Mode()
}

func (s *application) loadBatchThumbnail(ctx context.Context, path string) (image.Image, error) {
	return compress.ThumbnailWithAcquire(ctx, path, batchThumbnailPixels, func(info compress.ImageInfo) (func(), error) {
		return s.imageMemory.acquire(ctx, imageMemoryEstimate(info, true))
	})
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
