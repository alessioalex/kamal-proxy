package server

import (
	"sync"
)

// PerformConcurrently calls each function in a separate goroutine and waits for
// them to end.
func PerformConcurrently(fns ...func()) {
	var wg sync.WaitGroup

	for _, fn := range fns {
		wg.Go(fn)
	}

	wg.Wait()
}
