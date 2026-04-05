package engine_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"dojo/internal/engine"
)

func TestConcurrentExecution(t *testing.T) {
	t.Parallel()

	const maxWorkers = 3
	runner := engine.NewRunner(maxWorkers)

	var wg sync.WaitGroup
	var running atomic.Int32
	var maxRunning atomic.Int32

	numTasks := 30
	wg.Add(numTasks)

	for range numTasks {
		runner.Submit(func() {
			defer wg.Done()
			cur := running.Add(1)
			for {
				prev := maxRunning.Load()
				if cur <= prev || maxRunning.CompareAndSwap(prev, cur) {
					break
				}
			}
			running.Add(-1)
		})
	}

	wg.Wait()

	if peak := maxRunning.Load(); peak > int32(maxWorkers) {
		t.Errorf("peak concurrency %d exceeded maxWorkers %d", peak, maxWorkers)
	}
}
