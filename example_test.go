package netsec_test

import (
	"errors"
	"fmt"
	"net/netip"

	netsec "example.org/netsec/uid1300"
)

// ExampleSwitchHostIntegration shows the intended call pattern from a Go
// switching host: the control plane drives tickets, the data plane feeds ARP
// claims and consults the machine before forwarding each frame.
func ExampleMachine() {
	m := netsec.NewMachine()
	ip := netip.MustParseAddr("10.0.0.10")
	oldMAC, _ := netsec.ParseMAC("02:00:00:00:00:01")
	newMAC, _ := netsec.ParseMAC("02:00:00:00:00:02")

	// Control plane: establish the initial binding with generation 1.
	if err := m.Grant(netsec.Ticket{IP: ip, NewMAC: oldMAC, Generation: 1}); err != nil {
		panic(err)
	}
	// Data plane: only the ticket-authorized ARP confirms the binding.
	if err := m.HandleArpClaim(ip, oldMAC); err != nil {
		panic(err)
	}

	// Control plane: authorize a live migration with a higher generation.
	if err := m.Grant(netsec.Ticket{IP: ip, NewMAC: newMAC, Generation: 2}); err != nil {
		panic(err)
	}

	// During the migration window, frames keep going to the incumbent.
	fmt.Println(m.Lookup(ip))

	// A replayed generation-1 ticket is rejected.
	err := m.Grant(netsec.Ticket{IP: ip, NewMAC: oldMAC, Generation: 1})
	fmt.Println(errors.Is(err, netsec.ErrStaleTicket))

	// New MAC confirms the ticket and the binding switches atomically.
	if err := m.HandleArpClaim(ip, newMAC); err != nil {
		panic(err)
	}
	fmt.Println(m.Lookup(ip))

	// A late ARP from the old MAC is treated as an unauthorized takeover.
	err = m.HandleArpClaim(ip, oldMAC)
	fmt.Println(errors.Is(err, netsec.ErrArpDenied))
	fmt.Println(m.Snapshot(ip).State)
	fmt.Println(m.Lookup(ip).Action)

	// Output:
	// {deliver 02:00:00:00:00:01}
	// true
	// {deliver 02:00:00:00:00:02}
	// true
	// quarantined
	// drop
}
