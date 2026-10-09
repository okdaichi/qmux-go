package qmux

import (
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
)

func TestWriteLock_LockPriority(t *testing.T) {
	// Each case lists the waiters in the order they arrive while the lock
	// is held, and the order in which they then get it.
	tests := map[string]struct {
		waiters  []priority
		expected []int
	}{
		"lower urgency first": {
			waiters:  []priority{{urgency: 5, incremental: true}, {urgency: 1, incremental: true}, {urgency: 3, incremental: true}},
			expected: []int{1, 2, 0},
		},
		"control frames before stream data": {
			waiters:  []priority{{urgency: 0, incremental: true}, {urgency: urgencyControl, incremental: true}},
			expected: []int{1, 0},
		},
		"incremental streams in order of arrival": {
			waiters: []priority{
				{urgency: 3, incremental: true, streamID: 8},
				{urgency: 3, incremental: true, streamID: 0},
				{urgency: 3, incremental: true, streamID: 4},
			},
			expected: []int{0, 1, 2},
		},
		"non-incremental streams by stream ID, ahead of incremental ones": {
			waiters: []priority{
				{urgency: 3, incremental: true, streamID: 0},
				{urgency: 3, streamID: 12},
				{urgency: 3, streamID: 4},
			},
			expected: []int{2, 1, 0},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var lock writeLock
				lock.Lock()

				served := make(chan int, len(tt.waiters))
				for i, p := range tt.waiters {
					go func() {
						lock.LockPriority(p)
						served <- i
						lock.Unlock()
					}()
					// Let the waiter queue up before the next arrives.
					synctest.Wait()
				}
				lock.Unlock()
				synctest.Wait()

				got := make([]int, 0, len(tt.waiters))
				for range tt.waiters {
					got = append(got, <-served)
				}
				assert.Equal(t, tt.expected, got)

				// The lock is free again.
				assert.Nil(t, lock.acquire(priority{}))
			})
		})
	}
}
