package locking

import (
	"errors"
	"testing"
	"time"
)

func TestFSLockGroupTimesOutWithErrLockNotAcquired(t *testing.T) {
	var (
		lockDir  = t.TempDir()
		holding  = make(chan struct{})
		release  = make(chan struct{})
		heldDone = make(chan error, 1)
	)
	holder, err := NewFlockGroup(lockDir, time.Second)
	if err != nil {
		t.Fatalf("NewFlockGroup: %v", err)
	}
	waiter, err := NewFlockGroup(lockDir, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewFlockGroup: %v", err)
	}

	go func() {
		_, holdErr := holder.DoWithLock("key", func() (interface{}, error) {
			close(holding)
			<-release
			return nil, nil
		})
		heldDone <- holdErr
	}()
	<-holding

	start := time.Now()
	_, waitErr := waiter.DoWithLock("key", func() (interface{}, error) {
		t.Error("fn ran even though the lock is held")
		return nil, nil
	})
	if !errors.Is(waitErr, ErrLockNotAcquired) {
		t.Errorf("DoWithLock error = %v, want ErrLockNotAcquired", waitErr)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("DoWithLock waited %v, want about the 50ms timeout", elapsed)
	}

	close(release)
	if holdErr := <-heldDone; holdErr != nil {
		t.Fatalf("holder DoWithLock: %v", holdErr)
	}

	fnErr := errors.New("fn failed")
	if _, err := waiter.DoWithLock("key", func() (interface{}, error) { return nil, fnErr }); !errors.Is(err, fnErr) || errors.Is(err, ErrLockNotAcquired) {
		t.Errorf("DoWithLock error = %v, want fn's error once the lock is free", err)
	}
}
