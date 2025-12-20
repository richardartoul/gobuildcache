package dedupe

type Locker interface {
	DoWithLock(key string, fn func() (interface{}, error)) (v interface{}, err error)
}
