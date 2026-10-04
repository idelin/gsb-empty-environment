// Command demo exercises the acceptance boundary end to end:
// encode a GPS coordinate at resolution 8, expand its radius-3 k-ring,
// verify the disk is closed and hole-free, then run a saga whose
// mid-flow failure is reverse-compensated.
package main

import (
	"context"
	"errors"
	"fmt"

	"dishex/hexgrid"
	"dishex/saga"
)

func main() {
	// --- Part 1: discrete hexagonal index ---------------------------------
	lat, lng := 39.9042, 116.4074 // Beijing CBD
	const res = 8
	cell := hexgrid.FromLatLng(lat, lng, res)
	i, j := cell.IJ()
	fmt.Printf("GPS (%.4f, %.4f) @ res=%d -> cell %s\n", lat, lng, res, cell)
	fmt.Printf("  face=%d axial=(%d,%d)\n", cell.Face(), i, j)
	clat, clng := cell.CenterLatLng()
	fmt.Printf("  cell centre: (%.4f, %.4f)\n", clat, clng)
	fmt.Printf("  parent res=4: %s  parent res=0: %s\n",
		cell.Parent(4), cell.Parent(0))

	const k = 3
	disk := cell.KRing(k)
	fmt.Printf("k=%d ring disk: %d cells (formula 1+3k(k+1)=%d)\n",
		k, len(disk), 1+3*k*(k+1))

	// Closure check: interior cells must have all 6 neighbours in the disk.
	in := make(map[hexgrid.Cell]bool, len(disk))
	for _, c := range disk {
		in[c] = true
	}
	holes, disconnected := 0, 0
	for _, c := range disk {
		d, _ := cell.GridDistance(c)
		inner := 0
		for _, nb := range c.Neighbors() {
			if in[nb] {
				inner++
			} else if d < k {
				holes++
			}
		}
		if d == k && inner == 0 {
			disconnected++
		}
	}
	fmt.Printf("  closure: holes=%d disconnected-boundary=%d -> %s\n",
		holes, disconnected, map[bool]string{true: "CLOSED, NO GAPS", false: "BROKEN"}[holes == 0 && disconnected == 0])

	// --- Part 2: saga with reverse compensation ---------------------------
	fmt.Println("\nsaga: reserve -> authorize -> charge(fails) -> notify")
	var events []string
	book := func(name string) func(context.Context) error {
		return func(context.Context) error {
			events = append(events, "act:"+name)
			return nil
		}
	}
	undo := func(name string) func(context.Context) error {
		return func(context.Context) error {
			events = append(events, "cmp:"+name)
			return nil
		}
	}
	s := saga.New(
		saga.Step{Name: "reserve", Action: book("reserve"), Compensate: undo("reserve")},
		saga.Step{Name: "authorize", Action: book("authorize"), Compensate: undo("authorize")},
		saga.Step{Name: "charge", Action: func(context.Context) error {
			events = append(events, "act:charge")
			return errors.New("card declined")
		}, Compensate: undo("charge")},
		saga.Step{Name: "notify", Action: book("notify"), Compensate: undo("notify")},
	)
	err := s.Run(context.Background())
	fmt.Printf("  final status: %s\n", s.Status())
	fmt.Printf("  result: %v\n", err)
	fmt.Printf("  event order: %v\n", events)
	for _, tr := range s.Transitions() {
		fmt.Printf("  transition: %s -> %s (%s)\n", tr.From, tr.To, tr.Reason)
	}
}
