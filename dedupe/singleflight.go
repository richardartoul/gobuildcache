package dedupe

import "golang.org/x/sync/singleflight"

// SingleflightGroup is an implementation of Group that uses
// the golang.org/x/sync/singleflight library.
type SingleflightGroup struct {
	group singleflight.Group
}

// NewSingleflightGroup creates a new SingleflightGroup.
func NewSingleflightGroup() *SingleflightGroup {
	return &SingleflightGroup{}
}

// Do executes and returns the results of the given function using singleflight.
func (s *SingleflightGroup) Do(key string, fn func() (interface{}, error)) (v interface{}, err error, shared bool) {
	return s.group.Do(key, fn)
}
