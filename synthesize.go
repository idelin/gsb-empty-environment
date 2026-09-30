package alertseg

import (
	"math/rand"
	"sort"
	"time"
)

// Synthesize builds a deterministic fixture locally (no monitoring platform
// involved): sources flip between OK and FIRING at random offsets, with one
// suppression window in the middle. The same seed always yields the same
// fixture, so the output can be saved and replayed.
func Synthesize(seed int64, start time.Time, sources []string, eventsPerSource int) *Fixture {
	rng := rand.New(rand.NewSource(seed))
	var steps []FixtureStep

	for _, src := range sources {
		at := start
		state := StateFiring
		for i := 0; i < eventsPerSource; i++ {
			at = at.Add(time.Duration(1+rng.Intn(8)) * time.Minute)
			steps = append(steps, FixtureStep{At: at, Source: src, State: state})
			if state == StateFiring {
				state = StateOK
			} else {
				state = StateFiring
			}
		}
	}

	suppressAt := start.Add(5 * time.Minute)
	liftAt := start.Add(25 * time.Minute)
	on, off := true, false
	steps = append(steps,
		FixtureStep{At: suppressAt, Suppress: &on},
		FixtureStep{At: liftAt, Suppress: &off},
	)

	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At.Before(steps[j].At) })
	return &Fixture{
		Name:           "synthesized",
		RepeatInterval: "10m",
		Steps:          steps,
	}
}
