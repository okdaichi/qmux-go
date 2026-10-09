package qmux

import (
	"container/heap"
	"sync"
)

const (
	// urgencyControl ranks control frames ahead of all stream data.
	urgencyControl = -1
	// defaultUrgency is the urgency of a stream until SetPriority is
	// called, as in RFC 9218.
	defaultUrgency = 3
	maxUrgency     = 7
)

// priority ranks a writer that waits for the transport, in the terms of
// RFC 9218.
type priority struct {
	// urgency is served lowest first.
	urgency int8
	// incremental writers of one urgency take turns; the others are served
	// in the order of their stream ID, ahead of the incremental ones.
	incremental bool
	streamID    StreamID
}

type lockWaiter struct {
	priority
	// order breaks ties within an urgency: the stream ID, or the arrival
	// for an incremental writer.
	order uint64
	ready chan struct{}
}

// waiterHeap is a heap.Interface with the next writer to serve at its root.
type waiterHeap []*lockWaiter

func (h waiterHeap) Len() int      { return len(h) }
func (h waiterHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h waiterHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.urgency != b.urgency {
		return a.urgency < b.urgency
	}
	if a.incremental != b.incremental {
		return !a.incremental
	}
	return a.order < b.order
}

func (h *waiterHeap) Push(x any) { *h = append(*h, x.(*lockWaiter)) }

func (h *waiterHeap) Pop() any {
	old := *h
	w := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return w
}

// writeLock is a mutex that hands itself to its waiters by priority, so
// that when the transport is the bottleneck the most urgent stream writes
// the next record (Section 11 of the draft). A writer holds it for one
// record at a time.
type writeLock struct {
	mu       sync.Mutex
	held     bool
	arrivals uint64
	waiters  waiterHeap
}

// Lock acquires the lock ahead of all stream data, for control frames.
func (l *writeLock) Lock() {
	l.LockPriority(priority{urgency: urgencyControl, incremental: true})
}

// LockPriority acquires the lock, waiting behind the writers that rank
// before p.
func (l *writeLock) LockPriority(p priority) {
	if ready := l.acquire(p); ready != nil {
		<-ready
	}
}

// acquire takes the lock if it is free. Otherwise it returns a channel that
// is closed when the lock has been handed to the caller.
func (l *writeLock) acquire(p priority) <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		l.held = true
		return nil
	}
	w := &lockWaiter{priority: p, order: uint64(p.streamID), ready: make(chan struct{})}
	if p.incremental {
		l.arrivals++
		w.order = l.arrivals
	}
	heap.Push(&l.waiters, w)
	return w.ready
}

// Unlock releases the lock, or hands it to the waiter that ranks first.
func (l *writeLock) Unlock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.waiters) == 0 {
		l.held = false
		return
	}
	close(heap.Pop(&l.waiters).(*lockWaiter).ready)
}
