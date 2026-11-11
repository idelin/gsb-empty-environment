package llhls

import "time"

// Encoder drives a Playlist with a steady part cadence from a Clock. It
// models a real encoder: every tick publishes one partial segment, and the
// Playlist rolls a new segment once TargetDuration is reached. With a
// VirtualClock the whole pipeline runs on simulated time.
type Encoder struct {
	pl    *Playlist
	clock Clock
	tick  time.Duration
}

// NewEncoder builds an Encoder that publishes a part of dur every tick.
func NewEncoder(pl *Playlist, clock Clock, tick time.Duration) *Encoder {
	return &Encoder{pl: pl, clock: clock, tick: tick}
}

// Run blocks, publishing parts until stop is closed. It is intended to run in
// its own goroutine. The first part of every segment is marked independent.
func (e *Encoder) Run(stop <-chan struct{}) {
	timer := e.clock.NewTimer(e.tick)
	partInSegment := 0
	partsPerSegment := int(e.pl.cfg.TargetDuration / e.tick)

	for {
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C():
			e.pl.PublishPart(e.tick, partInSegment == 0)
			partInSegment++
			if partInSegment >= partsPerSegment {
				partInSegment = 0
			}
			timer = e.clock.NewTimer(e.tick)
		}
	}
}
