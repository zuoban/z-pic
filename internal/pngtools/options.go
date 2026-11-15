package pngtools

import "fmt"

// QuantizeOptions configures pngquant. The zero value selects all defaults;
// otherwise every field must be valid, with MinQuality=0 allowed explicitly.
type QuantizeOptions struct {
	MinQuality int
	MaxQuality int
	Colors     int
	Speed      int
}

// DefaultQuantizeOptions returns the default quality gate, palette size, and speed.
func DefaultQuantizeOptions() QuantizeOptions {
	return QuantizeOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: 3}
}

// Validate checks the complete configuration, accepting the zero value as defaults.
func (o QuantizeOptions) Validate() error {
	if o == (QuantizeOptions{}) {
		return nil
	}
	if o.MinQuality < 0 || o.MinQuality > 100 {
		return fmt.Errorf("PNG 最低质量必须在 0 到 100 之间")
	}
	if o.MaxQuality < 1 || o.MaxQuality > 100 {
		return fmt.Errorf("PNG 最高质量必须在 1 到 100 之间")
	}
	if o.MinQuality > o.MaxQuality {
		return fmt.Errorf("PNG 最低质量不能大于最高质量")
	}
	if o.Colors < 2 || o.Colors > 256 {
		return fmt.Errorf("PNG 颜色数量必须在 2 到 256 之间")
	}
	if o.Speed < 1 || o.Speed > 11 {
		return fmt.Errorf("PNG 量化速度必须在 1 到 11 之间")
	}
	return nil
}
