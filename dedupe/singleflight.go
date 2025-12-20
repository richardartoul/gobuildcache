package dedupe

import "sync"

type MemLock struct {
	sync.Mutex
	locks map[string]*sync.Mutex
}

func NewMemLock() *MemLock {
	return &MemLock{}
}

func (s *MemLock) DoWithLock(key string, fn func() (interface{}, error)) (v interface{}, err error) {
	s.Lock()
	lock, ok := s.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	s.Unlock()
	lock.Lock()
	defer lock.Unlock()
	return fn()
}
