package kurnhttp

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func largeArray(size int) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	item := `"` + strings.Repeat("x", 4094) + `"`
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// After a burst of large responses has been decoded and released by its
// caller, neither the inputs nor decoder buffers may stay reachable through
// the pool: one collection must return the heap to about where it was.
func TestLargeInputsAreNotRetainedByThePool(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	size := 0
	for i := 0; i < 4; i++ {
		big := largeArray(16 << 20)
		size = len(big)
		if _, err := array(big); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("4 inputs of %d bytes; heap retained after the burst and one GC: %d bytes", size, growth)
	if growth > 2<<20 {
		t.Fatalf("a burst of %d-byte inputs left %d bytes reachable after collection", size, growth)
	}
}
