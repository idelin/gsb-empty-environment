// Command demo replays the acceptance scenario of the streaming joiner on a
// deterministic fake clock: a matched pair, a cancelled wait, and a late
// right-side compensation.
package main

import (
	"fmt"
	"time"

	joiner "example.com/streamjoin"
)

var clock *joiner.FakeClock

func at() string {
	return fmt.Sprintf("t=%2ds", int(clock.Now().Sub(time.Unix(0, 0)).Seconds()))
}

func main() {
	clock = joiner.NewFakeClock(time.Unix(0, 0))
	j := joiner.New(clock, 5*time.Second, 5*time.Second, func(r joiner.Result) {
		fmt.Printf("%s  >>> output: %-11s key=%s\n", at(), r.Type, r.Key)
	})

	send := func(e joiner.Event) {
		kind := "data  "
		if e.Kind == joiner.Cancel {
			kind = "cancel"
		}
		fmt.Printf("%s  ingest %s/%s\n", at(), e.Side, kind)
		j.Ingest(e)
	}
	advance := func(d time.Duration) {
		clock.Advance(d)
		fmt.Printf("%s  ...\n", at())
	}

	fmt.Println("== scenario 1: L1 matched by R1 within the 5s window ==")
	send(joiner.Event{Key: "L1", Side: joiner.Left})  // t=0
	advance(3 * time.Second)                          // t=3
	send(joiner.Event{Key: "L1", Side: joiner.Right}) // immediate MATCHED
	advance(2 * time.Second)                          // t=5: deadline passes silently

	fmt.Println("== scenario 2: L2 cancelled before its timeout ==")
	advance(5 * time.Second) // t=10
	send(joiner.Event{Key: "L2", Side: joiner.Left})
	advance(2 * time.Second) // t=12
	send(joiner.Event{Key: "L2", Side: joiner.Left, Kind: joiner.Cancel})
	advance(3 * time.Second) // t=15: no orphan output for L2

	fmt.Println("== scenario 3: late R compensates an orphan L3 ==")
	advance(5 * time.Second) // t=20
	send(joiner.Event{Key: "L3", Side: joiner.Left})
	advance(5 * time.Second)                          // t=25: LEFT-ONLY
	send(joiner.Event{Key: "L3", Side: joiner.Right}) // LATE-MATCHED

	advance(time.Hour)
	fmt.Printf("%s  final retained state entries: %d (zero leak)\n", at(), j.StateSize())
}
