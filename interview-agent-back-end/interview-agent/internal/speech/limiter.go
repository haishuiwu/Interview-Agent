package speech

import (
	"fmt"
	"sync"
)

// Limiter combines a process-wide semaphore with a per-user counter.
type Limiter struct {
	global  chan struct{}
	perUser int

	mu    sync.Mutex
	users map[string]int
}

// LimitRejection identifies why a non-blocking acquire was rejected.
type LimitRejection uint8

const (
	LimitNotRejected LimitRejection = iota
	LimitInvalidUser
	LimitPerUser
	LimitGlobal
)

// NewLimiter creates a non-blocking in-process limiter.
func NewLimiter(globalLimit, perUserLimit int) (*Limiter, error) {
	if globalLimit <= 0 {
		return nil, fmt.Errorf("speech: global concurrency must be positive")
	}
	if perUserLimit <= 0 {
		return nil, fmt.Errorf("speech: per-user concurrency must be positive")
	}
	if perUserLimit > globalLimit {
		return nil, fmt.Errorf("speech: per-user concurrency cannot exceed global concurrency")
	}
	return &Limiter{
		global:  make(chan struct{}, globalLimit),
		perUser: perUserLimit,
		users:   make(map[string]int),
	}, nil
}

// Acquire reserves capacity without blocking. The returned release function is
// safe to call more than once.
func (l *Limiter) Acquire(userID string) (release func(), ok bool) {
	release, _, ok = l.AcquireDetailed(userID)
	return release, ok
}

// AcquireDetailed reserves capacity and reports the rejection boundary.
func (l *Limiter) AcquireDetailed(userID string) (release func(), rejection LimitRejection, ok bool) {
	if l == nil || userID == "" {
		return nil, LimitInvalidUser, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.users[userID] >= l.perUser {
		return nil, LimitPerUser, false
	}
	select {
	case l.global <- struct{}{}:
		l.users[userID]++
	default:
		return nil, LimitGlobal, false
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.users[userID] <= 1 {
				delete(l.users, userID)
			} else {
				l.users[userID]--
			}
			<-l.global
			l.mu.Unlock()
		})
	}, LimitNotRejected, true
}
