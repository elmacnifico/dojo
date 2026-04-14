package engine_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elmacnifico/dojo/internal/engine"
)

func TestNewRunnerClampsToOne(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{0, -1, -100} {
		var wg sync.WaitGroup
		wg.Add(1)
		var ran atomic.Bool
		r := engine.NewRunner(workers)
		r.Submit(func() {
			defer wg.Done()
			ran.Store(true)
		})
		wg.Wait()
		if !ran.Load() {
			t.Errorf("NewRunner(%d): task did not run", workers)
		}
	}
}

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
