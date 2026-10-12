package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func testAdmission(t *testing.T, limits, queues map[string]int, wait time.Duration) admission {
	t.Helper()
	a, err := newAdmission(registry{limits: limits, queues: queues}, wait)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A list with one slot and a queue of one: the running request holds the
// token, one more may wait, the next is refused at once with the queue
// bound while the waiter is still queued; cancellation and completion give
// positions back in order.
func TestQueueBoundRefusesPromptlyAndReleases(t *testing.T) {
	a := testAdmission(t, map[string]int{"l": 1}, map[string]int{"l": 1}, 50*time.Millisecond)
	first, err := a.enqueue([]string{"l"})
	if err != nil {
		t.Fatal(err)
	}
	release, _, err := a.acquire(context.Background(), []string{"l"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.enqueue([]string{"l"})
	if err != nil || a.waiting("l") != 1 {
		t.Fatalf("second enqueue: %v waiting %d", err, a.waiting("l"))
	}
	started := time.Now()
	if _, err := a.enqueue([]string{"l"}); !errors.Is(err, errQueueFull) {
		t.Fatalf("third enqueue: %v", err)
	}
	if time.Since(started) > 20*time.Millisecond {
		t.Fatal("queue-full refusal waited")
	}
	// The waiter blocks on the token and is released by cancellation; its
	// position is still held until it dequeues.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := a.acquire(ctx, []string{"l"})
		done <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	second()
	if a.waiting("l") != 0 {
		t.Fatalf("waiting after dequeue: %d", a.waiting("l"))
	}
	// With the waiter gone a new request may queue again; when the runner
	// finishes it is admitted.
	third, err := a.enqueue([]string{"l"})
	if err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	go func() {
		r, _, err := a.acquire(context.Background(), []string{"l"})
		if err == nil {
			r()
		}
		admitted <- err
	}()
	release()
	first()
	if err := <-admitted; err != nil {
		t.Fatalf("admission after release: %v", err)
	}
	third()
	if len(a.gates["l"].slots) != 0 || len(a.gates["l"].tokens) != 0 {
		t.Fatal("positions leaked")
	}
}

// Multi-list acquisition: a request needs a position on every list; when
// the second list is full nothing is held on the first.
func TestQueueMultiListRollback(t *testing.T) {
	a := testAdmission(t, map[string]int{"a": 1, "b": 1}, map[string]int{"a": 4, "b": 1}, 50*time.Millisecond)
	holdB, err := a.enqueue([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	releaseB, _, err := a.acquire(context.Background(), []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	waiterB, err := a.enqueue([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue([]string{"b", "a", "b"}); !errors.Is(err, errQueueFull) {
		t.Fatalf("multi-list enqueue: %v", err)
	}
	if len(a.gates["a"].slots) != 0 {
		t.Fatal("rollback left a position on the first list")
	}
	if _, err := a.enqueue([]string{"nope"}); !errors.Is(err, errInvalidService) {
		t.Fatalf("unknown list: %v", err)
	}
	waiterB()
	releaseB()
	holdB()
}

// No contention: enqueue and acquire never block and leave nothing behind.
func TestQueueNoContentionPath(t *testing.T) {
	a := testAdmission(t, map[string]int{"l": 4}, map[string]int{"l": 16}, time.Second)
	for i := 0; i < 100; i++ {
		dequeue, err := a.enqueue([]string{"l"})
		if err != nil {
			t.Fatal(err)
		}
		release, _, err := a.acquire(context.Background(), []string{"l"})
		if err != nil {
			t.Fatal(err)
		}
		release()
		dequeue()
	}
	if len(a.gates["l"].slots) != 0 || len(a.gates["l"].tokens) != 0 || a.waiting("l") != 0 {
		t.Fatal("positions leaked")
	}
}

// Under concurrent pressure the bound holds: never more than limit running,
// never more than limit+queue positions held, refusals are immediate, and
// everything is returned.
func TestQueueBoundUnderConcurrency(t *testing.T) {
	const limit, queue, workers = 2, 3, 64
	a := testAdmission(t, map[string]int{"l": limit}, map[string]int{"l": queue}, 200*time.Millisecond)
	var wg sync.WaitGroup
	var mu sync.Mutex
	running, maxRunning, refused, admitted, timedOut := 0, 0, 0, 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				dequeue, err := a.enqueue([]string{"l"})
				if err != nil {
					mu.Lock()
					refused++
					mu.Unlock()
					time.Sleep(time.Millisecond)
					continue
				}
				if held := len(a.gates["l"].slots); held > limit+queue {
					t.Errorf("positions held %d > %d", held, limit+queue)
				}
				release, _, err := a.acquire(context.Background(), []string{"l"})
				if err != nil {
					mu.Lock()
					timedOut++
					mu.Unlock()
					dequeue()
					continue
				}
				mu.Lock()
				running++
				if running > maxRunning {
					maxRunning = running
				}
				admitted++
				mu.Unlock()
				time.Sleep(200 * time.Microsecond)
				mu.Lock()
				running--
				mu.Unlock()
				release()
				dequeue()
			}
		}()
	}
	wg.Wait()
	if maxRunning > limit || admitted == 0 || refused == 0 {
		t.Fatalf("max running %d admitted %d refused %d timed out %d", maxRunning, admitted, refused, timedOut)
	}
	if len(a.gates["l"].slots) != 0 || len(a.gates["l"].tokens) != 0 {
		t.Fatal("positions leaked")
	}
}

func TestQueueConfigurationBounds(t *testing.T) {
	for _, bad := range []map[string]int{{"l": 0}, {"l": MaxServiceQueue + 1}, {}} {
		if _, err := newAdmission(registry{limits: map[string]int{"l": 1}, queues: bad}, time.Second); err == nil {
			t.Fatalf("queue bound %v accepted", bad)
		}
	}
}
