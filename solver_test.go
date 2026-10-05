package main

import (
	"strings"
	"testing"
)

// lineMatrix builds a travel time matrix from positions on a line (a metric,
// so the triangle inequality always holds).
func lineMatrix(pos ...int) [][]int {
	n := len(pos)
	m := make([][]int, n)
	for i := range m {
		m[i] = make([]int, n)
		for j := range m {
			d := pos[i] - pos[j]
			if d < 0 {
				d = -d
			}
			m[i][j] = d
		}
	}
	return m
}

// starMatrix builds a tree metric: every leaf i hangs off the depot (node 0)
// with the given arm length, so leaf-to-leaf travel goes via the depot.
func starMatrix(arms ...int) [][]int {
	n := len(arms)
	m := make([][]int, n)
	for i := range m {
		m[i] = make([]int, n)
		for j := range m {
			if i != j {
				m[i][j] = arms[i] + arms[j]
			}
		}
	}
	return m
}

func wide() Window { return Window{Open: 0, Close: 1000} }

func TestTriangleInequalityRejected(t *testing.T) {
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 1, Seats: 1,
				PickupWindow: wide(), DeliveryWindow: wide()},
		},
		// t[0][1]=10 > t[0][2]+t[2][1]=2
		Matrix: [][]int{{0, 10, 1}, {10, 0, 10}, {1, 1, 0}},
	}
	_, err := Solve(in)
	if err == nil || !strings.Contains(err.Error(), "triangle inequality") {
		t.Fatalf("expected triangle inequality rejection, got %v", err)
	}
}

func TestMinVehiclesThenShortestDistance(t *testing.T) {
	// depot@0, P_A@10, D_A@20, P_B@15, D_B@25
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 50, SeatCapacity: 2},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 10, Seats: 1, PickupWindow: wide(), DeliveryWindow: wide()},
			{ID: "B", Pickup: 3, Delivery: 4, Weight: 10, Seats: 1, PickupWindow: wide(), DeliveryWindow: wide()},
		},
		Matrix: lineMatrix(0, 10, 20, 15, 25),
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Vehicles != 1 {
		t.Fatalf("vehicles = %d, want 1", res.Vehicles)
	}
	// best single route: 0->1->3->2->4->0 = 10+5+5+5+25 = 50
	if res.TotalTravelTime != 50 {
		t.Fatalf("total travel = %d, want 50", res.TotalTravelTime)
	}
}

func TestCapacityForcesSecondVehicle(t *testing.T) {
	// Tree metric: depot--P_A(4)--D_A(8), depot--P_B(4)--D_B(8).
	// Both pickups must happen by t=12, deliveries not before t=20, so any
	// single-vehicle plan must carry both orders at once.
	matrix := [][]int{
		{0, 4, 12, 4, 12},
		{4, 0, 8, 8, 16},
		{12, 8, 0, 16, 24},
		{4, 8, 16, 0, 8},
		{12, 16, 24, 8, 0},
	}
	orders := []Order{
		{ID: "A", Pickup: 1, Delivery: 2, Weight: 1, Seats: 1,
			PickupWindow:   Window{Open: 0, Close: 12},
			DeliveryWindow: Window{Open: 20, Close: 200}},
		{ID: "B", Pickup: 3, Delivery: 4, Weight: 1, Seats: 1,
			PickupWindow:   Window{Open: 0, Close: 12},
			DeliveryWindow: Window{Open: 20, Close: 200}},
	}
	// Seat capacity 2: one vehicle carries both.
	res, err := Solve(&Input{
		Depot: 0, Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 2},
		Orders: orders, Matrix: matrix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Vehicles != 1 {
		t.Fatalf("cap=2: vehicles = %d, want 1", res.Vehicles)
	}
	// 0->P_B(4)->P_A(8)->D_A(16,wait)->D_B(24)->0(12) = 56
	if res.TotalTravelTime != 56 {
		t.Fatalf("cap=2: total travel = %d, want 56", res.TotalTravelTime)
	}
	// Seat capacity 1: overlap impossible, two vehicles required.
	res, err = Solve(&Input{
		Depot: 0, Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 1},
		Orders: orders, Matrix: matrix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Vehicles != 2 {
		t.Fatalf("cap=1: vehicles = %d, want 2", res.Vehicles)
	}
	// each: 0->P(4)->D(8,wait)->0(12) = 24
	if res.TotalTravelTime != 48 {
		t.Fatalf("cap=1: total travel = %d, want 48", res.TotalTravelTime)
	}
}

func TestPickupBeforeDeliveryAndNoTransfer(t *testing.T) {
	// Asymmetric metric: delivering first would be cheaper but is forbidden.
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 1, Seats: 1, PickupWindow: wide(), DeliveryWindow: wide()},
		},
		Matrix: [][]int{
			{0, 10, 12},
			{3, 0, 2},
			{1, 2, 0},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	// forced 0->P->D->0 = 10+2+1 = 13 (cheating order would be 12+2+3 = 17)
	if res.TotalTravelTime != 13 {
		t.Fatalf("total travel = %d, want 13", res.TotalTravelTime)
	}
	stops := res.Routes[0].Stops
	if len(stops) != 4 || stops[1].Action != "pickup" || stops[2].Action != "deliver" {
		t.Fatalf("unexpected stop sequence: %+v", stops)
	}
	if stops[1].Order != stops[2].Order || stops[1].Order != "A" {
		t.Fatalf("order changed vehicle mid-trip: %+v", stops)
	}
}

func TestWaitingTimeAndLoadReported(t *testing.T) {
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 4, Seats: 2, Service: 5,
				PickupWindow:   Window{Open: 30, Close: 100},
				DeliveryWindow: Window{Open: 0, Close: 200}},
		},
		Matrix: lineMatrix(0, 10, 30),
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	stops := res.Routes[0].Stops
	pu := stops[1]
	if pu.Action != "pickup" || pu.Arrival != 10 || pu.Wait != 20 || pu.Departure != 35 {
		t.Fatalf("pickup stop = %+v, want arrival 10 wait 20 departure 35", pu)
	}
	if pu.LoadWeight != 4 || pu.LoadSeats != 2 {
		t.Fatalf("load after pickup = (%d,%d), want (4,2)", pu.LoadWeight, pu.LoadSeats)
	}
	dl := stops[2]
	if dl.Action != "deliver" || dl.Arrival != 55 || dl.Wait != 0 || dl.Departure != 60 {
		t.Fatalf("delivery stop = %+v, want arrival 55 wait 0 departure 60", dl)
	}
	if dl.LoadWeight != 0 || dl.LoadSeats != 0 {
		t.Fatalf("load after delivery = (%d,%d), want (0,0)", dl.LoadWeight, dl.LoadSeats)
	}
	if res.TotalTravelTime != 10+20+30 {
		t.Fatalf("total travel = %d, want 60", res.TotalTravelTime)
	}
}

func TestUnservableOrderRejected(t *testing.T) {
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 1, Seats: 1,
				PickupWindow:   Window{Open: 0, Close: 5}, // depot->pickup is 10
				DeliveryWindow: wide()},
		},
		Matrix: lineMatrix(0, 10, 20),
	}
	if _, err := Solve(in); err == nil {
		t.Fatal("expected infeasible error, got nil")
	}
}

func TestTooManyOrdersRejected(t *testing.T) {
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Matrix:  lineMatrix(0, 1),
	}
	for i := 0; i < 11; i++ {
		in.Orders = append(in.Orders, Order{
			ID: "x", Pickup: 1, Delivery: 1, PickupWindow: wide(), DeliveryWindow: wide(),
		})
	}
	if _, err := Solve(in); err == nil || !strings.Contains(err.Error(), "too many orders") {
		t.Fatalf("expected too-many-orders error, got %v", err)
	}
}

func TestTimeWindowsSplitVehicles(t *testing.T) {
	// Tree metric: depot--P_A(10)--D_A(4), depot--P_B(10)--D_B(4).
	// Each order alone is servable, but both pickups close at t=12 while
	// visiting the second pickup takes 10+20=30, so one vehicle cannot
	// serve both.
	matrix := [][]int{
		{0, 10, 14, 10, 14},
		{10, 0, 4, 20, 24},
		{14, 4, 0, 24, 28},
		{10, 20, 24, 0, 4},
		{14, 24, 28, 4, 0},
	}
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Pickup: 1, Delivery: 2, Weight: 1, Seats: 1,
				PickupWindow:   Window{Open: 0, Close: 12},
				DeliveryWindow: Window{Open: 0, Close: 100}},
			{ID: "B", Pickup: 3, Delivery: 4, Weight: 1, Seats: 1,
				PickupWindow:   Window{Open: 0, Close: 12},
				DeliveryWindow: Window{Open: 0, Close: 100}},
		},
		Matrix: matrix,
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Vehicles != 2 {
		t.Fatalf("vehicles = %d, want 2", res.Vehicles)
	}
	// each: 0->P(10)->D(4)->0(14) = 28
	if res.TotalTravelTime != 56 {
		t.Fatalf("total travel = %d, want 56", res.TotalTravelTime)
	}
}

func TestEightOrderStress(t *testing.T) {
	// 8 orders on a star: pickups (arms 5..12) all close at t=30, deliveries
	// (arms 40..47) open at t=100, so each vehicle must finish all pickups
	// before any delivery and carries its whole group at once. Seat
	// capacity 2 therefore forces exactly 4 vehicles.
	arms := []int{0}
	var orders []Order
	for i := 0; i < 8; i++ {
		arms = append(arms, 5+i, 40+i)
		orders = append(orders, Order{
			ID:             string(rune('A' + i)),
			Pickup:         1 + 2*i,
			Delivery:       2 + 2*i,
			Weight:         5,
			Seats:          1,
			PickupWindow:   Window{Open: 0, Close: 30},
			DeliveryWindow: Window{Open: 100, Close: 1000},
		})
	}
	in := &Input{
		Depot:   0,
		Vehicle: VehicleSpec{WeightCapacity: 100, SeatCapacity: 2},
		Orders:  orders,
		Matrix:  starMatrix(arms...),
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Vehicles != 4 {
		t.Fatalf("vehicles = %d, want 4", res.Vehicles)
	}
	// Every order must appear exactly twice (pickup+deliver) across routes.
	count := make(map[string]int)
	for _, r := range res.Routes {
		for _, st := range r.Stops {
			if st.Order != "" {
				count[st.Order]++
			}
		}
	}
	if len(count) != 8 {
		t.Fatalf("served %d distinct orders, want 8", len(count))
	}
	for id, c := range count {
		if c != 2 {
			t.Fatalf("order %s visited %d times, want 2", id, c)
		}
	}
}
