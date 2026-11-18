package nameseq

import "testing"

func TestSequenceSkipsAndCompressesChains(t *testing.T) {
	var nilSeq Sequence
	if got := nilSeq.Next(0); got != 0 {
		t.Fatalf("nil Sequence.Next(0) = %d, want 0", got)
	}
	if got := nilSeq.Next(5); got != 5 {
		t.Fatalf("nil Sequence.Next(5) = %d, want 5", got)
	}

	seq := make(Sequence)
	for i := 0; i < 5; i++ {
		got := seq.Next(0)
		if got != i {
			t.Fatalf("step %d: Next(0) = %d, want %d", i, got, i)
		}
		seq[got] = got + 1
	}
	// Path compression links visited nodes along the traversed chain directly to 5.
	if got := seq.Next(0); got != 5 {
		t.Fatalf("Next(0) = %d, want 5", got)
	}
	if seq[0] != 5 || seq[4] != 5 {
		t.Fatalf("visited nodes not compressed: seq[0]=%d seq[4]=%d, want 5", seq[0], seq[4])
	}
	for i := 1; i < 4; i++ {
		if got := seq.Next(i); got != 5 || seq[i] != 5 {
			t.Fatalf("Next(%d) = %d (seq[%d]=%d), want 5", i, got, i, seq[i])
		}
	}
	// Disjoint gaps remain usable until reserved.
	seq[8] = 9
	if got := seq.Next(6); got != 6 {
		t.Fatalf("Next(6) = %d, want 6", got)
	}
	if got := seq.Next(8); got != 9 {
		t.Fatalf("Next(8) = %d, want 9", got)
	}
}
