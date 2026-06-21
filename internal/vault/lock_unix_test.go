//go:build unix

package vault

import (
	"testing"
	"time"
)

// A second lock attempt must block until the first is released, so concurrent
// mutations serialize instead of racing.
func TestLockStore_Serializes(t *testing.T) {
	dir := t.TempDir()
	unlock1, err := lockStore(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		unlock2, err := lockStore(dir) // must block while unlock1 is held
		if err != nil {
			t.Errorf("second lock: %v", err)
			return
		}
		close(acquired)
		unlock2()
	}()

	select {
	case <-acquired:
		t.Fatal("second lock was acquired while the first was still held")
	case <-time.After(100 * time.Millisecond):
		// still blocked, as expected
	}

	unlock1()
	select {
	case <-acquired: // now it can proceed
	case <-time.After(2 * time.Second):
		t.Fatal("second lock never acquired after the first was released")
	}
}
