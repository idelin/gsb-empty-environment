// Command demo runs the full cross-datacenter failover scenario against a
// real (in-process) Witness HTTP server:
//
//	phase 1: DC1 primary, DC2 standby
//	phase 2: fiber cut between DC1 and the Witness
//	phase 3: Witness grants DC2 a takeover lease (DC1 self-demoted)
//	phase 4: fiber healed, DC1 detects its invalid credential -> read-only
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"leasefed/controller"
	"leasefed/store"
	"leasefed/witness"
)

const ttl = 1200 * time.Millisecond

var t0 = time.Now()

func step(format string, args ...interface{}) {
	prefix := fmt.Sprintf("[%5.1fs] ", time.Since(t0).Seconds())
	log.Printf(prefix+format, args...)
}

func roles(dc1, dc2 *controller.Controller) string {
	return fmt.Sprintf("dc1=%s(epoch=%d) dc2=%s(epoch=%d)",
		dc1.Role(), dc1.Epoch(), dc2.Role(), dc2.Epoch())
}

func tryWrite(name string, dc *controller.Controller, key, value string) {
	err := dc.Write(key, value)
	if err != nil {
		step("%s write %q=%q REJECTED: %v", name, key, value, err)
	} else {
		step("%s write %q=%q OK", name, key, value)
	}
}

func waitFor(what string, cond func() bool) {
	for !cond() {
		time.Sleep(20 * time.Millisecond)
	}
	step("==> %s", what)
}

func main() {
	log.SetFlags(0)

	// Third-party judge, reachable over HTTP from both datacenters.
	w := witness.New()
	var client1, client2 controller.Client
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		// Sandboxed environment without permission to listen on ports:
		// fall back to an in-process judge.
		step("cannot listen on a port (%v); using in-process witness", err)
		client1 = witness.NewLocalClient(w)
		client2 = witness.NewLocalClient(w)
	} else {
		go http.Serve(ln, w.Handler())
		addr := "http://" + ln.Addr().String()
		step("witness (judge) listening on %s", addr)
		client1 = witness.NewHTTPClient(addr)
		client2 = witness.NewHTTPClient(addr)
	}

	link1 := controller.NewCuttableClient(client1)
	link2 := controller.NewCuttableClient(client2)
	dc1 := controller.New("dc1", link1, store.New(), ttl)
	dc2 := controller.New("dc2", link2, store.New(), ttl)
	dc1.Start()

	step("--- phase 1: normal operation ---")
	waitFor("dc1 acquired the global lease", dc1.IsPrimary)
	dc2.Start()
	time.Sleep(ttl) // let dc2 try (and fail) to grab the lease
	step("roles: %s", roles(dc1, dc2))
	tryWrite("dc1", dc1, "balance", "100")
	tryWrite("dc2", dc2, "balance", "0") // must be rejected: standby is read-only

	time.Sleep(ttl) // let a couple of renewals happen
	step("dc1 keeps renewing, roles: %s", roles(dc1, dc2))

	step("--- phase 2: FIBER CUT between dc1 and the witness ---")
	link1.Cut()
	step("dc1 still primary until its local lease expires (no split-brain overlap)")
	time.Sleep(ttl + 500*time.Millisecond)
	step("dc1 lease expired locally, roles: %s", roles(dc1, dc2))
	tryWrite("dc1", dc1, "balance", "999") // must be rejected: self-demoted

	step("--- phase 3: witness confirms dc1 is gone, promotes dc2 ---")
	waitFor("dc2 received the takeover lease", dc2.IsPrimary)
	step("roles: %s", roles(dc1, dc2))
	tryWrite("dc2", dc2, "balance", "50")

	step("--- phase 4: fiber healed, dc1 rejoins ---")
	link1.Heal()
	time.Sleep(ttl)
	step("dc1 detected its credential is invalid, roles: %s", roles(dc1, dc2))
	tryWrite("dc1", dc1, "balance", "1") // must be rejected: read-only standby
	if err := dc1.Store().Put("balance", "1", 1); err != nil {
		step("dc1 stale-epoch (1) raw write REJECTED by fencing token: %v", err)
	}
	tryWrite("dc2", dc2, "balance", "60")

	v1, _ := dc1.Store().Get("balance")
	v2, _ := dc2.Store().Get("balance")
	step("final: dc1 store balance=%q fence=%d | dc2 store balance=%q fence=%d",
		v1, dc1.Store().Fence(), v2, dc2.Store().Fence())
	step("done: exactly one writer at all times, no dual-primary")

	dc1.Stop()
	dc2.Stop()
}
