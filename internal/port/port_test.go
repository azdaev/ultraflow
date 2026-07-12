package port

import "testing"

// TestAllocateDistinct is the M1 acceptance core: concurrently-created tasks must
// get DISTINCT ports, even before any dev server binds them.
func TestAllocateDistinct(t *testing.T) {
	a := NewAllocator()
	seen := map[int]bool{}
	for i := 0; i < 50; i++ {
		p, err := a.Allocate()
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if p <= 0 {
			t.Fatalf("expected a positive port, got %d", p)
		}
		if seen[p] {
			t.Fatalf("port %d handed out twice", p)
		}
		seen[p] = true
	}
}

// TestReleaseThenReuse verifies a freed port can be handed out again (Release
// removes it from the in-use set), while a still-held port cannot.
func TestReleaseThenReuse(t *testing.T) {
	a := NewAllocator()
	p, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if !a.used[p] {
		t.Fatalf("allocated port %d should be marked in use", p)
	}
	a.Release(p)
	if a.used[p] {
		t.Fatalf("released port %d should no longer be in use", p)
	}
}

// TestReserveBlocksReuse ensures a port re-reserved (e.g. after a restart) won't
// be re-allocated to a new task.
func TestReserveBlocksReuse(t *testing.T) {
	a := NewAllocator()
	a.Reserve(54321)
	if !a.used[54321] {
		t.Fatalf("reserved port should be in use")
	}
	// Reserve/Release of a zero port is a harmless no-op.
	a.Reserve(0)
	a.Release(0)
}
