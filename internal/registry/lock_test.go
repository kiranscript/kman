package registry

import "testing"

// TestLookupTakesNoLocks holds the lock writers share and checks a lookup
// still finishes: the request path must never wait on an admin change
// (SPEC §1, invariant 5). If it did, this test would hang until go test's
// timeout, which names the blocked goroutine.
func TestLookupTakesNoLocks(t *testing.T) {
	r := New()
	r.Apply(Change{Generation: 1, Keys: []Key{{ID: "abcd1234", Generation: 1}}})
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan bool, 1)
	go func() {
		_, ok := r.Lookup("abcd1234")
		done <- ok
	}()
	if ok := <-done; !ok {
		t.Error("the key is not there")
	}
}
