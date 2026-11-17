// Package nameseq tracks occupied filename suffixes within one operation.
package nameseq

// Sequence links occupied indexes to the next candidate. Callers record only
// names they reserved themselves; external filesystem entries must be rechecked.
type Sequence map[int]int

// Next skips linked indexes and compresses the visited path for later lookups.
// A nil Sequence has no reservations.
func (sequence Sequence) Next(index int) int {
	available := index
	for sequence[available] > available {
		available = sequence[available]
	}
	for sequence[index] > index {
		following := sequence[index]
		sequence[index] = available
		index = following
	}
	return available
}
