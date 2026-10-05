package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
)

// Input JSON schema:
// {
//   "defaults":  {"entry_buffer": 5, "exit_buffer": 5},
//   "spots":     [{"id": "A1", "vehicle_types": ["sedan", "suv"]}],
//   "reservations": [{
//     "id": "R1", "vehicle_type": "sedan",
//     "start": 480, "end": 540,
//     "entry_buffer": 10, "exit_buffer": 0,   // optional, overrides defaults
//     "profit": 120, "confirmed": false
//   }]
// }
// Times are integer minutes on an arbitrary shared axis.
// Occupancy is the half-open interval
//   [start - entry_buffer, end + exit_buffer).
// Two reservations conflict only if their occupancy intervals overlap
// with positive length; touching endpoints (a.end == b.start) do NOT
// conflict (left-closed, right-open rule).

type Defaults struct {
	EntryBuffer int64 `json:"entry_buffer"`
	ExitBuffer  int64 `json:"exit_buffer"`
}

type Spot struct {
	ID           string   `json:"id"`
	VehicleTypes []string `json:"vehicle_types"`
}

type Reservation struct {
	ID          string `json:"id"`
	VehicleType string `json:"vehicle_type"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`
	EntryBuffer *int64 `json:"entry_buffer,omitempty"`
	ExitBuffer  *int64 `json:"exit_buffer,omitempty"`
	Profit      int64  `json:"profit"`
	Confirmed   bool   `json:"confirmed"`
}

type Input struct {
	Defaults     Defaults      `json:"defaults"`
	Spots        []Spot        `json:"spots"`
	Reservations []Reservation `json:"reservations"`
}

const maxReservations = 25

func readInput(r io.Reader) (*Input, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return &in, nil
}

// order is the solver-internal view of a reservation.
type order struct {
	id        string
	vtype     string
	reqStart  int64 // requested start (before buffers)
	reqEnd    int64 // requested end (before buffers)
	start     int64 // occupancy start (entry buffer applied)
	end       int64 // occupancy end (exit buffer applied)
	profit    int64
	confirmed bool
	compat    []int // indices of compatible spots
}

// overlap reports whether half-open intervals [a1,a2) and [b1,b2)
// intersect with positive length.
func overlap(a1, a2, b1, b2 int64) bool {
	return a1 < b2 && b1 < a2
}

func buildOrders(in *Input) ([]order, []string, error) {
	if len(in.Reservations) == 0 {
		return nil, nil, fmt.Errorf("no reservations given")
	}
	if len(in.Reservations) > maxReservations {
		return nil, nil, fmt.Errorf("too many reservations: %d > %d", len(in.Reservations), maxReservations)
	}
	if len(in.Spots) == 0 {
		return nil, nil, fmt.Errorf("no parking spots given")
	}
	spotIDs := make([]string, len(in.Spots))
	spotTypes := make([]map[string]bool, len(in.Spots))
	for i, s := range in.Spots {
		if s.ID == "" {
			return nil, nil, fmt.Errorf("spot #%d has empty id", i)
		}
		spotIDs[i] = s.ID
		m := make(map[string]bool, len(s.VehicleTypes))
		for _, t := range s.VehicleTypes {
			m[t] = true
		}
		spotTypes[i] = m
	}
	seen := make(map[string]bool)
	orders := make([]order, 0, len(in.Reservations))
	for _, r := range in.Reservations {
		if r.ID == "" {
			return nil, nil, fmt.Errorf("a reservation has empty id")
		}
		if seen[r.ID] {
			return nil, nil, fmt.Errorf("duplicate reservation id %q", r.ID)
		}
		seen[r.ID] = true
		if r.End <= r.Start {
			return nil, nil, fmt.Errorf("reservation %q: end (%d) must be after start (%d)", r.ID, r.End, r.Start)
		}
		eb := in.Defaults.EntryBuffer
		if r.EntryBuffer != nil {
			eb = *r.EntryBuffer
		}
		xb := in.Defaults.ExitBuffer
		if r.ExitBuffer != nil {
			xb = *r.ExitBuffer
		}
		if eb < 0 || xb < 0 {
			return nil, nil, fmt.Errorf("reservation %q: buffers must be non-negative", r.ID)
		}
		o := order{
			id:        r.ID,
			vtype:     r.VehicleType,
			reqStart:  r.Start,
			reqEnd:    r.End,
			start:     r.Start - eb,
			end:       r.End + xb,
			profit:    r.Profit,
			confirmed: r.Confirmed,
		}
		for i := range in.Spots {
			if spotTypes[i][r.VehicleType] {
				o.compat = append(o.compat, i)
			}
		}
		orders = append(orders, o)
	}
	return orders, spotIDs, nil
}
