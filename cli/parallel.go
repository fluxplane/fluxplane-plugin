package cli

import (
	"runtime"
	"sync"
)

// fanoutLimit is the default bound on concurrent plugin invocations for commands
// that fan out across many plugins. Plugin calls spawn a process each, so we cap
// concurrency to avoid thundering the machine while still collapsing the serial
// wall-clock of discovery/health commands.
func fanoutLimit() int {
	n := runtime.NumCPU()
	if n < 4 {
		return 4
	}
	if n > 12 {
		return 12
	}
	return n
}

// runConcurrent invokes fn for each index in [0,n) using at most `limit` workers.
// fn must only write to its own index i (no shared mutation), so no locking is
// needed at the call sites. Order is preserved by index.
func runConcurrent(n, limit int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if limit <= 0 {
		limit = fanoutLimit()
	}
	if n == 1 || limit == 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	if limit > n {
		limit = n
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
