package engine

import (
	"sync"
)

// Registry is a thread-safe map holding the currently running test contexts.
type Registry struct {
	mu    sync.RWMutex
	tests map[string]*ActiveTest
}

// NewRegistry creates an initialized test worker Registry.
func NewRegistry() *Registry {
	return &Registry{
		tests: make(map[string]*ActiveTest),
	}
}

// Register adds a new ActiveTest into the map.
func (r *Registry) Register(id string, test *ActiveTest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tests[id] = test
}

// Lookup safely retrieves an active test by its registry key (test folder name).
func (r *Registry) Lookup(id string) (*ActiveTest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	test, ok := r.tests[id]
	return test, ok
}

// Unregister removes an ActiveTest from the registry.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tests, id)
}

// ForEach iterates all registered tests under a read lock. The callback
// receives each (id, test) pair; return false to stop early.
func (r *Registry) ForEach(fn func(string, *ActiveTest) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, at := range r.tests {
		if !fn(id, at) {
			return
		}
	}
}

// Count returns the number of currently registered active tests.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tests)
}
