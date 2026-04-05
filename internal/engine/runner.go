package engine

// Runner provides a concurrent worker pool for running tasks.
type Runner struct {
	sem chan struct{}
}

// NewRunner creates a new bounded concurrency runner.
func NewRunner(maxWorkers int) *Runner {
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
