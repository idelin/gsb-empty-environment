package eventbus

import "time"

// Sweep finalizes the two-phase cleanup: every listener whose subscription
// handle was reclaimed by the GC is removed from the bus. Listeners that
// have been pending longer than the configured zombie timeout are reported
// as zombies (a leaked-subscriber warning) before being force-removed.
//
// It returns how many listeners were swept and how many of them were
// zombies. Call it periodically (see StartWatchdog) or on demand.
func (b *Bus) Sweep() (swept, zombies int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	for id, entry := range b.pending {
		age := now.Sub(entry.since)
		if age >= b.zombieAge {
			b.logger.Printf("WARN zombie subscription detected: id=%d topic=%q dead for %s without being swept; force-unsubscribing",
				id, entry.topic, age.Round(time.Millisecond))
			zombies++
		}
		b.removeLocked(id, entry.topic)
		delete(b.pending, id)
		b.cleaned++
		swept++
	}
	return swept, zombies
}

// StartWatchdog launches a background goroutine that sweeps the bus every
// interval, surfacing zombie-subscription warnings as they occur. The
// returned function stops the watchdog.
func (b *Bus) StartWatchdog(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				b.Sweep()
			}
		}
	}()
	return func() { close(done) }
}
