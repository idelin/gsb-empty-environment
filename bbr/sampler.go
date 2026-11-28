package bbr

// bwSample is one delivery-rate (deliveryRate) sample.
type bwSample struct {
	// rate is delivered bytes per second for the sample interval.
	rate uint64
	// round is the BBR round in which the sample was produced.
	round uint64
	// appLimited marks a sample taken while the application could not keep
	// the pipe full; such samples must never pull down bw_hi.
	appLimited bool
}

// rateSampler implements the BBR max-bandwidth window filter. It keeps the
// largest delivery rate observed over the last MaxBwFilterWindow rounds and
// retains the most recent full-app-utilization sample as a sticky fallback
// when every sample inside the window is app-limited.
type rateSampler struct {
	samples []bwSample

	// maxBw is the filtered bw_hi estimate (bytes/sec).
	maxBw uint64
	// fallbackRate is the latest non-app-limited sample, used when the whole
	// window contains only app-limited samples.
	fallbackRate uint64
	windowRounds int
}

func newRateSampler(windowRounds int) *rateSampler {
	return &rateSampler{windowRounds: windowRounds}
}
func (s *rateSampler) reset() {
	s.samples = nil
	s.maxBw = 0
	s.fallbackRate = 0
}

// update folds a new delivery-rate sample into the window filter and returns
// the refreshed bw_hi. App-limited samples are stored for bookkeeping but can
// never raise (or lower) the filtered maximum.
func (s *rateSampler) update(rate uint64, round uint64, appLimited bool) uint64 {
	s.samples = append(s.samples, bwSample{rate: rate, round: round, appLimited: appLimited})
	if !appLimited && rate > s.fallbackRate {
		s.fallbackRate = rate
	}
	s.prune(round)

	best := uint64(0)
	for _, smp := range s.samples {
		if !smp.appLimited && smp.rate > best {
			best = smp.rate
		}
	}
	if best == 0 {
		// Every sample in the window is app-limited: keep the previous
		// estimate rather than letting an idle application collapse bw_hi.
		best = s.fallbackRate
		if best == 0 {
			best = s.maxBw
		}
	}
	s.maxBw = best
	return s.maxBw
}

func (s *rateSampler) prune(currentRound uint64) {
	cutoff := int64(currentRound) - int64(s.windowRounds) + 1
	if cutoff <= 0 {
		return
	}
	kept := s.samples[:0]
	for _, smp := range s.samples {
		if int64(smp.round) >= cutoff {
			kept = append(kept, smp)
		}
	}
	s.samples = kept
}

// bwHi returns the current filtered maximum delivery rate (bytes/sec).
func (s *rateSampler) bwHi() uint64 { return s.maxBw }
