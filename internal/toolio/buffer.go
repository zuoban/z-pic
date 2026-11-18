package toolio

// BoundedBuffer accumulates process stdout up to a known maximum retainable size
// without overshooting maxCap during geometric slice growth.
type BoundedBuffer struct {
	data []byte
}

func (b *BoundedBuffer) Len() int { return len(b.data) }

func (b *BoundedBuffer) Cap() int { return cap(b.data) }

func (b *BoundedBuffer) Bytes() []byte { return b.data }

func (b *BoundedBuffer) String() string { return string(b.data) }

func (b *BoundedBuffer) Reset() { b.data = nil }

// AppendBounded appends prefix (if non-empty) followed by chunk, clamping
// capacity growth to maxCap when maxCap >= needed.
func (b *BoundedBuffer) AppendBounded(prefix, chunk []byte, maxCap int) {
	needed := len(b.data) + len(prefix) + len(chunk)
	if needed == len(b.data) {
		return
	}
	if needed > cap(b.data) {
		var newCap int
		if cap(b.data) == 0 {
			newCap = 2 * needed
		} else if cap(b.data) < 4<<20 {
			newCap = 4 * cap(b.data)
		} else {
			newCap = 2 * cap(b.data)
		}
		if maxCap >= needed && newCap > maxCap {
			newCap = maxCap
		}
		if newCap < needed {
			newCap = needed
		}
		grown := make([]byte, len(b.data), newCap)
		copy(grown, b.data)
		b.data = grown
	}
	if len(prefix) > 0 {
		b.data = append(b.data, prefix...)
	}
	b.data = append(b.data, chunk...)
}
