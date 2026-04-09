package engine

// Runner provides a concurrent worker pool for running tasks.
type Runner struct {
	sem chan struct{}
}

// NewRunner creates a new bounded concurrency runner. Values below 1 are
// clamped to 1 so callers never trigger a panic or deadlock.
func NewRunner(maxWorkers int) *Runner {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	return &Runner{
		sem: make(chan struct{}, maxWorkers),
	}
}

// Submit enqueues a task to run, blocking if maxWorkers are already executing.
func (r *Runner) Submit(task func()) {
	r.sem <- struct{}{} // acquire token
	go func() {
		defer func() { <-r.sem }() // release token
		task()
	}()
}
