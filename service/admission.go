package service

import (
	"context"
	"errors"
	"sort"
	"time"
)

// gate is one physical list's admission: tokens are the in-service slots
// (max_inflight); slots are the in-service plus waiting positions, so the
// number waiting never exceeds the list's queue bound. A request holds a
// slot from enqueue until it finishes, and a token only while it runs.
type gate struct{ tokens, slots chan struct{} }

type admission struct {
	gates map[string]*gate
	wait  time.Duration
}

var errQueueFull = errors.New("service: admission queue full")

func newAdmission(registry registry, wait time.Duration) (admission, error) {
	if wait <= 0 || wait > MaxAdmissionWait {
		return admission{}, errInvalidService
	}
	result := admission{gates: make(map[string]*gate, len(registry.limits)), wait: wait}
	for name, limit := range registry.limits {
		queue := registry.queues[name]
		if name == "" || limit < 1 || queue < 1 || queue > MaxServiceQueue {
			return admission{}, errInvalidService
		}
		result.gates[name] = &gate{tokens: make(chan struct{}, limit), slots: make(chan struct{}, limit+queue)}
	}
	return result, nil
}

func canonicalNames(names []string) []string {
	canonical := append([]string(nil), names...)
	sort.Strings(canonical)
	for index := 1; index < len(canonical); index++ {
		if canonical[index] == canonical[index-1] {
			canonical = append(canonical[:index], canonical[index+1:]...)
			index--
		}
	}
	return canonical
}

// enqueue takes a waiting-or-running position on every named list without
// blocking. It fails with errQueueFull, holding nothing, when any list's
// positions are all taken; the caller then refuses at once. The returned
// function gives the positions back and must run after acquire's release.
func (a admission) enqueue(names []string) (func(), error) {
	canonical := canonicalNames(names)
	held := make([]*gate, 0, len(canonical))
	rollback := func() {
		for index := len(held) - 1; index >= 0; index-- {
			<-held[index].slots
		}
	}
	for _, name := range canonical {
		item := a.gates[name]
		if item == nil {
			rollback()
			return nil, errInvalidService
		}
		select {
		case item.slots <- struct{}{}:
			held = append(held, item)
		default:
			rollback()
			return nil, errQueueFull
		}
	}
	return rollback, nil
}

// waiting reports how many requests currently hold a position on the list
// without a running token (observability for tests and counters).
func (a admission) waiting(name string) int {
	item := a.gates[name]
	if item == nil {
		return 0
	}
	return len(item.slots) - len(item.tokens)
}

func (a admission) acquire(ctx context.Context, names []string) (func(), time.Duration, error) {
	canonical := canonicalNames(names)
	acquired := make([]*gate, 0, len(canonical))
	var admissionTimer <-chan time.Time
	timer := time.NewTimer(a.wait)
	defer timer.Stop()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > a.wait {
		admissionTimer = timer.C
	}
	rollback := func() {
		for index := len(acquired) - 1; index >= 0; index-- {
			<-acquired[index].tokens
		}
	}
	for _, name := range canonical {
		item := a.gates[name]
		if item == nil {
			rollback()
			return nil, 0, errInvalidService
		}
		select {
		case item.tokens <- struct{}{}:
			acquired = append(acquired, item)
		case <-ctx.Done():
			rollback()
			return nil, 0, ctx.Err()
		case <-admissionTimer:
			rollback()
			return nil, retryHint(a.wait), errAdmissionUnavailable
		}
	}
	return func() { rollback() }, 0, nil
}

func retryHint(value time.Duration) time.Duration {
	seconds := (value + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 5 {
		seconds = 5
	}
	return seconds * time.Second
}
