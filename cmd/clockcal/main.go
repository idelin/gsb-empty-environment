// Command clockcal solves multi-channel clock calibration: each channel maps
// local time t to global time a*t + b with a rational drift rate a and an
// integer offset b. Calibration points carry error intervals; the tool
// computes the exact feasible parameter ranges per channel, isolates
// incompatible calibration points, and derives the partial order of aligned
// events without ever forcing a total order by midpoints.
//
// Usage: clockcal [input.json]   (reads stdin when no file is given)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"clockcal/internal/calib"
	"clockcal/internal/order"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "clockcal:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var r io.Reader = os.Stdin
	if len(args) > 2 {
		return fmt.Errorf("usage: clockcal [input.json]")
	}
	if len(args) == 2 && args[1] != "-" {
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	var in Input
	dec := json.NewDecoder(r)
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("decoding input: %w", err)
	}
	rep, err := Solve(&in)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// Solve runs the full calibration pipeline and builds the report.
func Solve(in *Input) (*Report, error) {
	rep := &Report{}
	var events []order.Event

	for ci := range in.Channels {
		ch := &in.Channels[ci]
		aBounds, err := ch.DriftBounds.interval()
		if err != nil {
			return nil, fmt.Errorf("channel %q: %v", ch.Name, err)
		}
		cs, err := ch.constraints()
		if err != nil {
			return nil, fmt.Errorf("channel %q: %v", ch.Name, err)
		}

		kept, dropped := calib.MaxCompatibleSubset(cs, aBounds)
		reg := calib.Solve(kept, aBounds)

		cr := ChannelReport{
			Name:    ch.Name,
			Drift:   newIntervalJSON(reg.A),
			Offset:  newIntervalJSON(reg.B),
			IntFeas: reg.IntFeasible,
			Used:    constraintIDs(kept),
			Dropped: droppedReports(dropped),
		}
		if reg.IntFeasible {
			cr.OffsetIntMin = intStr(reg.BIntLo)
			cr.OffsetIntMax = intStr(reg.BIntHi)
		}
		switch {
		case !reg.Feasible:
			cr.Status = "infeasible"
		case len(dropped) > 0:
			cr.Status = "resolved_by_dropping"
		case len(cs) == 0:
			cr.Status = "no_calibration"
		default:
			cr.Status = "ok"
		}
		rep.Channels = append(rep.Channels, cr)

		for ei, ev := range ch.Events {
			id := ev.ID
			if id == "" {
				id = fmt.Sprintf("%s:e%d", ch.Name, ei)
			}
			tau, err := ev.Local.rat()
			if err != nil {
				return nil, fmt.Errorf("channel %q event %q: %v", ch.Name, id, err)
			}
			var iv calib.Interval
			if reg.IntFeasible {
				iv = calib.EventInterval(kept, aBounds, reg, tau)
			}
			events = append(events, order.Event{ID: id, Channel: ch.Name, Interval: iv})
			rep.Events = append(rep.Events, EventReport{
				ID: id, Channel: ch.Name, Local: tau.RatString(),
				Global: newIntervalJSON(iv),
			})
		}
	}

	rel := order.Build(events)
	for _, e := range rel.TransitiveReduction() {
		rep.PartialOrder.Edges = append(rep.PartialOrder.Edges, Edge{
			Before: events[e[0]].ID, After: events[e[1]].ID,
		})
	}
	for _, p := range rel.Incomparable() {
		rep.PartialOrder.Incomparable = append(rep.PartialOrder.Incomparable,
			[2]string{events[p[0]].ID, events[p[1]].ID})
	}
	if rep.PartialOrder.Edges == nil {
		rep.PartialOrder.Edges = []Edge{}
	}
	if rep.PartialOrder.Incomparable == nil {
		rep.PartialOrder.Incomparable = [][2]string{}
	}
	return rep, nil
}
