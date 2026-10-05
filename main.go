// Command carpool solves a dial-a-ride / carpool planning problem:
// up to 10 orders, each with a pickup and a delivery stop, weight and
// passenger (seat) demand, and time windows on both stops. Every order
// must be picked up before it is delivered and may not be transferred
// between vehicles. The objective is lexicographic: first minimize the
// number of vehicles, then the total travel distance.
//
// Input is a JSON document on stdin (or a file named by argv[1]):
//
//	{
//	  "vehicle": {"weight_capacity": 100, "seat_capacity": 4},
//	  "depot_departure": 0,
//	  "orders": [
//	    {"id": "A", "weight": 10, "passengers": 1,
//	     "pickup_window":  {"earliest": 0, "latest": 50},
//	     "delivery_window": {"earliest": 0, "latest": 120},
//	     "pickup_service": 0, "delivery_service": 0}
//	  ],
//	  "travel_time": [[0, 5, ...], ...]
//	}
//
// Node numbering of the travel-time matrix: node 0 is the depot, node
// i (1..n) is the pickup of order i, node n+i is the delivery of order i.
// The matrix therefore has dimension (2n+1) x (2n+1).
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// TimeWindow is a half-open service window: a vehicle may arrive at any
// time <= Latest; if it arrives before Earliest it waits.
type TimeWindow struct {
	Earliest int `json:"earliest"`
	Latest   int `json:"latest"`
}

// Order is a single ride/parcel request from its pickup to its delivery.
type Order struct {
	ID              string     `json:"id"`
	Weight          int        `json:"weight"`
	Passengers      int        `json:"passengers"`
	PickupWindow    TimeWindow `json:"pickup_window"`
	DeliveryWindow  TimeWindow `json:"delivery_window"`
	PickupService   int        `json:"pickup_service"`
	DeliveryService int        `json:"delivery_service"`
}

// Vehicle describes the homogeneous fleet capacity.
type Vehicle struct {
	WeightCapacity int `json:"weight_capacity"`
	SeatCapacity   int `json:"seat_capacity"`
}

// Input is the full problem instance.
type Input struct {
	Vehicle        Vehicle `json:"vehicle"`
	DepotDeparture int     `json:"depot_departure"`
	Orders         []Order `json:"orders"`
	TravelTime     [][]int `json:"travel_time"`
}

// StopOutput reports one visited stop: when the vehicle arrived, how
// long it waited for the window to open, and the load after service.
type StopOutput struct {
	Node           int    `json:"node"`
	Order          string `json:"order"`
	Kind           string `json:"kind"` // "pickup" or "delivery"
	Arrival        int    `json:"arrival"`
	Wait           int    `json:"wait"`
	Depart         int    `json:"depart"`
	LoadWeight     int    `json:"load_weight"`
	LoadPassengers int    `json:"load_passengers"`
}

// RouteOutput is one vehicle's tour, starting and ending at the depot.
type RouteOutput struct {
	Vehicle  int          `json:"vehicle"`
	Distance int          `json:"distance"`
	Stops    []StopOutput `json:"stops"`
}

// Output is the solver result.
type Output struct {
	Vehicles      int           `json:"vehicles"`
	TotalDistance int           `json:"total_distance"`
	Optimal       bool          `json:"optimal"`
	Warnings      []string      `json:"warnings,omitempty"`
	Routes        []RouteOutput `json:"routes"`
}

func (in *Input) validate() error {
	n := len(in.Orders)
	if n == 0 {
		return fmt.Errorf("no orders given")
	}
	if n > 10 {
		return fmt.Errorf("too many orders: %d (maximum is 10)", n)
	}
	if in.Vehicle.WeightCapacity <= 0 || in.Vehicle.SeatCapacity <= 0 {
		return fmt.Errorf("vehicle capacities must be positive")
	}
	dim := 2*n + 1
	if len(in.TravelTime) != dim {
		return fmt.Errorf("travel_time must be a %dx%d matrix (depot + %d pickups + %d deliveries), got %d rows",
			dim, dim, n, n, len(in.TravelTime))
	}
	for i, row := range in.TravelTime {
		if len(row) != dim {
			return fmt.Errorf("travel_time row %d has %d entries, want %d", i, len(row), dim)
		}
		for j, v := range row {
			if v < 0 {
				return fmt.Errorf("travel_time[%d][%d] is negative", i, j)
			}
		}
	}
	for i := range in.Orders {
		o := &in.Orders[i]
		if o.ID == "" {
			o.ID = fmt.Sprintf("order-%d", i+1)
		}
		if o.Weight < 0 || o.Passengers < 0 {
			return fmt.Errorf("order %q has negative demand", o.ID)
		}
		if o.Weight > in.Vehicle.WeightCapacity || o.Passengers > in.Vehicle.SeatCapacity {
			return fmt.Errorf("order %q (weight %d, passengers %d) exceeds vehicle capacity (%d, %d)",
				o.ID, o.Weight, o.Passengers, in.Vehicle.WeightCapacity, in.Vehicle.SeatCapacity)
		}
		if o.PickupWindow.Earliest > o.PickupWindow.Latest {
			return fmt.Errorf("order %q has an empty pickup window", o.ID)
		}
		if o.DeliveryWindow.Earliest > o.DeliveryWindow.Latest {
			return fmt.Errorf("order %q has an empty delivery window", o.ID)
		}
	}
	return nil
}

// triangleInequalityHolds reports whether t[i][j] <= t[i][k]+t[k][j] for
// all i, j, k. Distance-based pruning in the solver is only sound when
// this holds, so the caller must disable it otherwise.
func triangleInequalityHolds(t [][]int) bool {
	d := len(t)
	for i := 0; i < d; i++ {
		for j := 0; j < d; j++ {
			if i == j {
				continue
			}
			for k := 0; k < d; k++ {
				if t[i][j] > t[i][k]+t[k][j] {
					return false
				}
			}
		}
	}
	return true
}

func fatal(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	out, _ := json.Marshal(map[string]string{"error": msg})
	fmt.Fprintln(os.Stderr, string(out))
	os.Exit(1)
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("reading input: %v", err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("parsing input JSON: %v", err)
	}
	if err := in.validate(); err != nil {
		fatal("invalid input: %v", err)
	}

	ti := triangleInequalityHolds(in.TravelTime)
	s := newSolver(&in, ti)

	// An order that cannot be served even alone can never be served.
	// This inference itself relies on the triangle inequality: without
	// it a detour could be faster than the direct trip, so skip it then.
	if ti {
		for i := range in.Orders {
			if _, _, ok := in.simulate([]int{i, i + s.n}); !ok {
				fatal("order %q is infeasible: its delivery window cannot be met even by a dedicated vehicle", in.Orders[i].ID)
			}
		}
	}

	s.solve()
	if !s.hasBest {
		fatal("no feasible solution exists")
	}

	out := s.output()
	enc, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(enc))
}
