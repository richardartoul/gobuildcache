package dedupe

// Group is an interface for deduplicating concurrent requests.
// It ensures that only one execution is in-flight for a given key at a time.
type Group interface {
	// Do executes and returns the results of the given function, making sure that
	// only one execution is in-flight for a given key at a time. If a duplicate
	// comes in, the duplicate caller waits for the original to complete and
	// receives the same results. The return value shared indicates whether v was
	// given to multiple callers.
	Do(key string, fn func() (interface{}, error)) (v interface{}, err error, shared bool)
}
