package service

import "sync"

// MemoryObserver is a deterministic, dependency-free observer intended for
// conformance and embedding tests. Events contain only the closed service
// label vocabulary and numeric measurements.
type MemoryObserver struct {
	mu     sync.Mutex
	events []Event
}

func (o *MemoryObserver) Observe(event Event) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *MemoryObserver) Events() []Event {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Event(nil), o.events...)
}
