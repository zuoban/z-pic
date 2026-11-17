package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"zpic/internal/compress"
)

func TestImageMemoryBudgetBlocksAndCancels(t *testing.T) {
	budget := newImageMemoryBudget(100)
	release, err := budget.acquire(context.Background(), 80)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	smallRelease, err := budget.acquire(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	smallRelease()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := budget.acquire(ctx, 21); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("excess memory must wait for capacity: %v", err)
	}
	release()
	release()
	largeRelease, err := budget.acquire(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if budget.used != 100 {
		t.Fatalf("oversize jobs must reserve the entire budget, got %d", budget.used)
	}
	largeRelease()
	if budget.used != 0 {
		t.Fatal("released work must restore all capacity")
	}
}

func TestImageMemoryBudgetWakesWaitingWork(t *testing.T) {
	budget := newImageMemoryBudget(100)
	release, err := budget.acquire(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		releaseNext, err := budget.acquire(ctx, 100)
		if err == nil {
			releaseNext()
		}
		finished <- err
	}()
	release()
	if err := <-finished; err != nil {
		t.Fatalf("releasing memory must wake a waiter: %v", err)
	}
}

func TestImageMemoryEstimateAccountsForFormatAndDimensions(t *testing.T) {
	info := compress.ImageInfo{Format: "jpeg", Width: 4000, Height: 3000, Size: 4 << 20}
	jpeg := imageMemoryEstimate(info, false)
	info.Format = "png"
	png := imageMemoryEstimate(info, false)
	thumbnail := imageMemoryEstimate(info, true)
	if png <= jpeg || thumbnail >= png {
		t.Fatalf("unexpected memory estimates: jpeg=%d png=%d thumbnail=%d", jpeg, png, thumbnail)
	}
	info.Width, info.Height = 8000, 4000
	if imageMemoryEstimate(info, false) <= imageMemoryLimit {
		t.Fatal("maximum-size PNG work should run exclusively")
	}
	for _, invalid := range []compress.ImageInfo{
		{}, {Width: 1 << 62, Height: 2}, {Width: 2, Height: 2, Size: -1},
	} {
		if imageMemoryEstimate(invalid, false) != imageMemoryLimit {
			t.Fatal("unknown or invalid sizes must reserve the full budget")
		}
	}
}
