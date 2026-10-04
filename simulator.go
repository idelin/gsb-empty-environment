package main

import "fmt"

// Delivery is one scheduled message: a request plus how many times the
// retry loop re-sends it.
type Delivery struct {
	Req     AppendRequest
	Repeats int
}

// Simulator delivers append requests to a replica in a fixed (possibly
// out-of-order, possibly duplicated) sequence, collecting every event.
type Simulator struct {
	replica *Replica
}

func NewSimulator(r *Replica) *Simulator { return &Simulator{replica: r} }

// Deliver runs the schedule in order. Each delivery is sent 1+Repeats
// times to model retries that duplicate requests.
func (s *Simulator) Deliver(schedule []Delivery) {
	for i, d := range schedule {
		for n := 0; n <= d.Repeats; n++ {
			tag := fmt.Sprintf("req#%d", i+1)
			if n > 0 {
				tag = fmt.Sprintf("req#%d(retry %d)", i+1, n)
			}
			s.replica.events = append(s.replica.events,
				fmt.Sprintf("-- deliver %s: prev=(%d,t%d) entries=%s commit=%d leaderTerm=%d",
					tag, d.Req.PrevLogIndex, d.Req.PrevLogTerm,
					formatLog(d.Req.Entries), d.Req.CommitIndex, d.Req.LeaderTerm))
			s.replica.HandleAppend(d.Req)
		}
	}
}
