package dedupe

// NoOpGroup is a Group implementation that performs no deduplication.
// Every call executes the function immediately. This is useful for testing
// or scenarios where deduplication is not needed.
type NoOpGroup struct{}

// NewNoOpGroup creates a new NoOpGroup.
func NewNoOpGroup() *NoOpGroup {
	return &NoOpGroup{}
}

// Do executes the function immediately without any deduplication.
// The shared return value is always false since no deduplication occurs.
func (n *NoOpGroup) Do(key string, fn func() (interface{}, error)) (v interface{}, err error, shared bool) {
	v, err = fn()
	return v, err, false
}
