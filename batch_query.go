package main

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
)

func (s *application) batchFilterActive() bool {
	return s.batch != nil && (s.batch.filter != batchFilterAll || strings.TrimSpace(s.batch.query) != "")
}

func normalizeBatchQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(filepath.ToSlash(query)))
}

func matchesBatchStatus(status batchStatus, filter int) bool {
	switch filter {
	case batchFilterPending:
		return status == batchPending || status == batchProcessing
	case batchFilterDone:
		return status == batchDone
	case batchFilterFailed:
		return status == batchFailed
	default:
		return true
	}
}

func matchesBatchFilter(item batchItem, filter int, query string) bool {
	if !matchesBatchStatus(item.status, filter) {
		return false
	}
	normalizedQuery := normalizeBatchQuery(query)
	if normalizedQuery == "" {
		return true
	}
	return strings.Contains(strings.ToLower(filepath.ToSlash(item.relPath)), normalizedQuery)
}

func snapshotOfBatchItem(item batchItem) batchItemSnapshot {
	return batchItemSnapshot{
		relPath:      item.relPath,
		originalSize: item.originalSize,
		outputSize:   item.outputSize,
		status:       item.status,
		unchanged:    item.unchanged,
		invalidInput: item.invalidInput,
	}
}

func samePendingBucketChange(prev, curr batchItemSnapshot) bool {
	if prev.relPath != curr.relPath ||
		prev.originalSize != curr.originalSize ||
		prev.outputSize != curr.outputSize ||
		prev.unchanged != curr.unchanged ||
		prev.invalidInput != curr.invalidInput {
		return false
	}
	prevPending := prev.status == batchPending || prev.status == batchProcessing
	currPending := curr.status == batchPending || curr.status == batchProcessing
	return prevPending && currPending
}

func (state *batchState) syncItemsCache() {
	orderUnchanged := false
	if state.cacheReady && len(state.cachedStates) == len(state.items) {
		unchanged := true
		onlyPendingTransitions := true
		for index, item := range state.items {
			snap := snapshotOfBatchItem(item)
			prev := state.cachedStates[index]
			if snap != prev {
				unchanged = false
				if !samePendingBucketChange(prev, snap) {
					onlyPendingTransitions = false
					break
				}
			}
		}
		if unchanged {
			return
		}
		orderUnchanged = onlyPendingTransitions
	}
	count := len(state.items)
	if cap(state.cachedStates) < count {
		state.cachedStates = make([]batchItemSnapshot, count)
	} else {
		state.cachedStates = state.cachedStates[:count]
	}
	if cap(state.cachedNameKeys) < count {
		grown := make([]batchNameKey, count)
		copy(grown, state.cachedNameKeys)
		state.cachedNameKeys = grown
	} else {
		state.cachedNameKeys = state.cachedNameKeys[:count]
	}
	var summary batchStatsSummary
	summary.total = count
	for index, item := range state.items {
		state.cachedStates[index] = snapshotOfBatchItem(item)
		key := &state.cachedNameKeys[index]
		if key.raw != item.relPath || (item.relPath == "" && key.slash != "") {
			slash := filepath.ToSlash(item.relPath)
			*key = batchNameKey{
				raw:   item.relPath,
				slash: slash,
				lower: strings.ToLower(slash),
			}
		}
		summary.origTotal += item.originalSize
		if !item.invalidInput {
			summary.canRun = true
		}
		switch item.status {
		case batchPending, batchProcessing:
			summary.pending++
		case batchDone:
			summary.done++
			summary.outTotal += item.outputSize
			summary.doneOrigTotal += item.originalSize
			if item.unchanged {
				summary.unchangedDone++
			}
		case batchFailed:
			summary.failed++
			if item.invalidInput {
				summary.invalidFailed++
			} else {
				summary.retryableFailed++
			}
		}
	}
	state.cachedStats = summary
	state.cacheReady = true
	if !orderUnchanged {
		state.visibleReady = false
		state.floatingReady = false
	}
}

func (state *batchState) filteredIndices(filter, sortMode int, query string) []int {
	state.syncItemsCache()
	if state.visibleReady && state.visibleFilter == filter && state.visibleSort == sortMode && state.visibleQuery == query {
		return state.cachedVisible
	}
	normalizedQuery := normalizeBatchQuery(query)
	visible := make([]int, 0, len(state.items))
	for index, item := range state.items {
		if !matchesBatchStatus(item.status, filter) {
			continue
		}
		if normalizedQuery != "" && !strings.Contains(state.cachedNameKeys[index].lower, normalizedQuery) {
			continue
		}
		visible = append(visible, index)
	}
	sortBatchIndicesWithKeys(state.items, state.cachedNameKeys, visible, sortMode)
	state.cachedVisible = visible
	state.visibleFilter = filter
	state.visibleSort = sortMode
	state.visibleQuery = query
	state.visibleReady = true
	return visible
}

func (state *batchState) floatingIndices(filter, sortMode int) []int {
	state.syncItemsCache()
	if state.floatingReady && state.floatingFilter == filter && state.floatingSort == sortMode {
		return state.cachedFloating
	}
	visible := make([]int, 0, len(state.items))
	for index, item := range state.items {
		if matchesBatchStatus(item.status, filter) {
			visible = append(visible, index)
		}
	}
	sortBatchIndicesWithKeys(state.items, state.cachedNameKeys, visible, sortMode)
	state.cachedFloating = visible
	state.floatingFilter = filter
	state.floatingSort = sortMode
	state.floatingReady = true
	return visible
}

func (s *application) filteredBatchIndices() []int {
	if s.batch == nil {
		return nil
	}
	return s.batch.filteredIndices(s.batch.filter, s.batch.sort, s.batch.query)
}

func sortBatchIndices(items []batchItem, indices []int, sortMode int) {
	if sortMode == batchSortDefault || len(indices) < 2 {
		return
	}
	keys := make([]batchNameKey, len(items))
	for index, item := range items {
		slash := filepath.ToSlash(item.relPath)
		keys[index] = batchNameKey{raw: item.relPath, slash: slash, lower: strings.ToLower(slash)}
	}
	sortBatchIndicesWithKeys(items, keys, indices, sortMode)
}

func sortBatchIndicesWithKeys(items []batchItem, keys []batchNameKey, indices []int, sortMode int) {
	if sortMode == batchSortDefault || len(indices) < 2 {
		return
	}
	slices.SortFunc(indices, func(left, right int) int {
		switch sortMode {
		case batchSortName:
			return compareBatchNameKeys(keys, left, right)
		case batchSortOriginalSize:
			if diff := cmp.Compare(items[right].originalSize, items[left].originalSize); diff != 0 {
				return diff
			}
			return compareBatchNameKeys(keys, left, right)
		case batchSortSavings:
			rankLeft, rankRight := batchSavingsRank(items[left]), batchSavingsRank(items[right])
			if diff := cmp.Compare(rankLeft, rankRight); diff != 0 {
				return diff
			}
			if rankLeft == 0 {
				if diff := compareBatchSavingsRatioDesc(items[left], items[right]); diff != 0 {
					return diff
				}
				return compareBatchNameKeys(keys, left, right)
			}
			if rankLeft == 1 {
				return compareBatchNameKeys(keys, left, right)
			}
			return cmp.Compare(left, right)
		default:
			return cmp.Compare(left, right)
		}
	})
}

func compareBatchNameKeys(keys []batchNameKey, left, right int) int {
	if diff := cmp.Compare(keys[left].lower, keys[right].lower); diff != 0 {
		return diff
	}
	if diff := cmp.Compare(keys[left].slash, keys[right].slash); diff != 0 {
		return diff
	}
	return cmp.Compare(left, right)
}

func compareBatchItemsByName(items []batchItem, a, b int) int {
	aRel, bRel := filepath.ToSlash(items[a].relPath), filepath.ToSlash(items[b].relPath)
	if c := cmp.Compare(strings.ToLower(aRel), strings.ToLower(bRel)); c != 0 {
		return c
	}
	if c := cmp.Compare(aRel, bRel); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

func batchSavingsRank(item batchItem) int {
	switch item.status {
	case batchDone:
		if !item.unchanged && item.originalSize > 0 && item.outputSize < item.originalSize {
			return 0
		}
		return 1
	case batchPending, batchProcessing:
		return 2
	default:
		return 3
	}
}

func compareBatchSavingsRatioDesc(a, b batchItem) int {
	aSaved := a.originalSize - a.outputSize
	bSaved := b.originalSize - b.outputSize
	if a.originalSize <= 1<<30 && b.originalSize <= 1<<30 {
		if c := cmp.Compare(bSaved*a.originalSize, aSaved*b.originalSize); c != 0 {
			return c
		}
	} else {
		aRatio := float64(aSaved) / float64(a.originalSize)
		bRatio := float64(bSaved) / float64(b.originalSize)
		if c := cmp.Compare(bRatio, aRatio); c != 0 {
			return c
		}
	}
	return cmp.Compare(bSaved, aSaved)
}

func (s *application) resetBatchFilter() {
	if s.batch == nil {
		return
	}
	s.batch.filter = batchFilterAll
	s.batch.query = ""
}

func (s *application) retryableFailedCount() int {
	retryable, _ := s.batchFailureCounts()
	return retryable
}

func (s *application) batchFailureCounts() (retryable, invalid int) {
	if s.batch == nil {
		return 0, 0
	}
	s.batch.syncItemsCache()
	return s.batch.cachedStats.retryableFailed, s.batch.cachedStats.invalidFailed
}

func (s *application) batchStats() (total, pending, done, failed int, origTotal, outTotal int64, canRun bool) {
	if s.batch == nil {
		return
	}
	s.batch.syncItemsCache()
	stats := s.batch.cachedStats
	return stats.total, stats.pending, stats.done, stats.failed, stats.origTotal, stats.outTotal, stats.canRun
}
