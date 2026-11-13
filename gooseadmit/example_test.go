package gooseadmit_test

import (
	"encoding/hex"
	"fmt"
	"time"

	"example.org/netsec/q0715/gooseadmit"
)

// onRawFrame is the shape of a layer-2 subscription callback: it receives the
// whole Ethernet frame and must decide, in order, whether the application is
// allowed to consume it.
func onRawFrame(ctrl *gooseadmit.Controller, raw []byte) {
	switch res := ctrl.HandleFrame(raw); res.Verdict {
	case gooseadmit.VerdictNewEvent:
		fmt.Println("new event applied")
	case gooseadmit.VerdictRetransmission:
		fmt.Println("retransmission accepted")
	case gooseadmit.VerdictDuplicate:
		fmt.Println("duplicate ignored")
	default:
		fmt.Printf("frame rejected: %d\n", res.Reason)
	}
}

func ExampleController() {
	clk := newManualClock()
	ctrl := gooseadmit.New(&gooseadmit.Config{Now: clk.now})

	// Provision each legitimate GOOSE control block as a four-tuple.
	mac, _ := hex.DecodeString("020000000001")
	key := gooseadmit.NewPublisherKey(
		mac,
		"ied1/LLN0/GoCB1",
		"ied1/LLN0/DS1",
		"GO1",
	)
	ctrl.Register(key)

	// A captured GOOSE frame (stNum=1, sqNum=0, ttl=100 ms, sim=false).
	raw, _ := hex.DecodeString(
		"00000000000002000000000188b83044800f696564312f4c4c4e302f476f" +
			"434231810164820d696564312f4c4c4e302f4453318303474f31840800" +
			"000000000000008501018601008701008801018901008a0101",
	)

	onRawFrame(ctrl, raw)          // first frame establishes the baseline
	onRawFrame(ctrl, raw)          // identical frame: duplicate, ignored
	fmt.Println(ctrl.IsValid(key)) // liveness held by the accepted frame

	clk.advance(101 * time.Millisecond)
	fmt.Println(ctrl.IsValid(key)) // timeAllowedToLive elapsed: invalid

	// Output:
	// new event applied
	// duplicate ignored
	// true
	// false
}

type manualClock struct{ t time.Time }

func newManualClock() *manualClock {
	return &manualClock{t: time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)}
}

func (m *manualClock) now() time.Time          { return m.t }
func (m *manualClock) advance(d time.Duration) { m.t = m.t.Add(d) }
