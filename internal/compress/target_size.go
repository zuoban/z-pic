package compress

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const (
	MinTargetSizeBytes int64 = 1 << 10
	MaxTargetSizeBytes int64 = maxInputSize
)

var (
	errTargetSizeRange = errors.New("目标体积必须为 0（不限制）或 1 KiB 到 64 MiB 之间")
	targetSizePattern  = regexp.MustCompile(`^([0-9]*\.?[0-9]+)[\s\p{Zs}]*([a-zA-Z]*)$`)
)

// ValidateTargetSizeBytes accepts zero (disabled) or a supported byte limit.
func ValidateTargetSizeBytes(size int64) error {
	if size != 0 && (size < MinTargetSizeBytes || size > MaxTargetSizeBytes) {
		return errTargetSizeRange
	}
	return nil
}

// ParseTargetSize parses a byte limit with optional decimal or binary units.
func ParseTargetSize(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	parts := targetSizePattern.FindStringSubmatch(raw)
	if parts == nil {
		return 0, errors.New("目标体积格式无效，请输入如 300KB 或 1.5MiB 的值")
	}
	var multiplier int64
	switch strings.ToLower(parts[2]) {
	case "", "b":
		multiplier = 1
	case "kb":
		multiplier = 1000
	case "mb":
		multiplier = 1_000_000
	case "kib", "k":
		multiplier = 1 << 10
	case "mib", "m":
		multiplier = 1 << 20
	default:
		return 0, fmt.Errorf("不支持的目标体积单位：%s", parts[2])
	}

	number := parts[1]
	if multiplier == 1 {
		whole, fraction, _ := strings.Cut(number, ".")
		if strings.Trim(fraction, "0") != "" {
			return 0, errors.New("目标体积的字节数必须为整数")
		}
		number = whole
	} else {
		// Preserve exact half-byte boundaries when rounding decimal quantities.
		value, ok := new(big.Rat).SetString(number)
		if !ok {
			return 0, errTargetSizeRange
		}
		number = value.Mul(value, big.NewRat(multiplier, 1)).FloatString(0)
	}
	size, err := strconv.ParseInt(number, 10, 64)
	if err != nil || size == 0 {
		return 0, errTargetSizeRange
	}
	if err := ValidateTargetSizeBytes(size); err != nil {
		return 0, err
	}
	return size, nil
}

// FormatTargetSize displays a byte limit, preferring exact unit multiples.
func FormatTargetSize(size int64) string {
	const kib, mib = 1 << 10, 1 << 20
	switch {
	case size <= 0:
		return ""
	case size%mib == 0:
		return fmt.Sprintf("%d MiB", size/mib)
	case size%kib == 0:
		return fmt.Sprintf("%d KiB", size/kib)
	case size%1_000_000 == 0:
		return fmt.Sprintf("%d MB", size/1_000_000)
	case size%1000 == 0:
		return fmt.Sprintf("%d KB", size/1000)
	case size >= mib:
		return fmt.Sprintf("%.2f MiB", float64(size)/mib)
	case size >= kib:
		return fmt.Sprintf("%.1f KiB", float64(size)/kib)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func encodeWithTargetSize(ctx context.Context, options Options, encode func(int) ([]byte, error)) ([]byte, int, error) {
	encoded, err := encode(options.Quality)
	if err != nil || options.Lossless || options.TargetSizeBytes == 0 {
		return encoded, 0, err
	}
	return searchTargetSize(ctx, encoded, options.Quality, options.TargetSizeBytes, func(data []byte) int {
		return len(data)
	}, encode)
}

// Higher ranks correspond to higher quality. Track the smallest output as a
// fallback because even the lowest rank can exceed the target.
func searchTargetSize[T any](ctx context.Context, initial T, initialRank int, target int64, size func(T) int, encode func(int) (T, error)) (T, int, error) {
	if int64(size(initial)) <= target {
		return initial, initialRank, nil
	}
	var bestFit T
	bestRank := 0
	smallest, smallestRank := initial, initialRank
	for low, high := 1, initialRank-1; low <= high; {
		if err := ctx.Err(); err != nil {
			return initial, 0, err
		}
		rank := low + (high-low)/2
		candidate, err := encode(rank)
		if err != nil {
			return initial, 0, err
		}
		length := size(candidate)
		if length < size(smallest) || (length == size(smallest) && rank > smallestRank) {
			smallest, smallestRank = candidate, rank
		}
		if int64(length) <= target {
			bestFit, bestRank = candidate, rank
			low = rank + 1
		} else {
			high = rank - 1
		}
	}
	if err := ctx.Err(); err != nil {
		return initial, 0, err
	}
	if bestRank != 0 {
		return bestFit, bestRank, nil
	}
	return smallest, smallestRank, nil
}
