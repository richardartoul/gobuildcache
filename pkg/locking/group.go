package locking

import "errors"

// ErrLockNotAcquired is returned (wrapped) by DoWithLock when the lock could not be
// acquired, in which case the function was not run. It lets callers tell lock
// failures apart from errors returned by the function itself.
var ErrLockNotAcquired = errors.New("failed to acquire lock")

// locking.Group is an abstraction for running functions with mutual exclusion
// over sets of keys.
type Group interface {
	// DoWithLock runs the given function with mutual exclusion over the given key.
	DoWithLock(key string, fn func() (interface{}, error)) (v interface{}, err error)
}
