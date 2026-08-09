package collector

import (
	"context"
	"log"
	"sync"
	"time"
)

// Scheduler runs each Collector on its own interval, concurrently.
type Scheduler struct {
	interval   time.Duration
	collectors []Collector
}

func NewScheduler(interval time.Duration, collectors ...Collector) *Scheduler {
	return &Scheduler{interval: interval, collectors: collectors}
}

// Run starts every collector in its own goroutine and blocks until ctx is
// cancelled, then waits for all of them to stop before returning.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range s.collectors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runCollector(ctx, c)
		}()
	}
	wg.Wait()
}

func (s *Scheduler) runCollector(ctx context.Context, c Collector) {
	s.collectOnce(ctx, c) // collect immediately so /metrics isn't empty at startup

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collectOnce(ctx, c)
		}
	}
}

func (s *Scheduler) collectOnce(ctx context.Context, c Collector) {
	if err := c.Collect(ctx); err != nil {
		log.Printf("collector %q failed: %v", c.Name(), err)
	}
}
