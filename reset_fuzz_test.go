package netsec

import (
	"bytes"
	"testing"
	"time"
)

func FuzzDetectResetNeverClosesOnUnmatchedPacket(f *testing.F) {
	f.Add([]byte{0x40, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20})
	f.Add([]byte{0x00})
	f.Add([]byte{0xc0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, packet []byte) {
		clock := NewManualClock(time.Unix(1800, 0).UTC())
		m := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0xf1}, 8), nil, "edge-a", "c1"))
		grant, err := m.IssueCID()
		if err != nil {
			t.Fatal(err)
		}
		probe := func(p []byte) bool { return len(p) > 0 && p[0]&1 == 1 }
		d := m.DetectReset(packet, ResetOptions{Route: "edge-a", Probe: probe})
		if d.Closed {
			if len(packet) < MinStatelessResetPacketSize ||
				packet[0]&0x40 == 0 ||
				packet[0]&0x80 != 0 ||
				probe(packet) {
				t.Fatalf("closed on packet failing shape/probe checks: %+v", d)
			}
			if !bytes.Equal(packet[len(packet)-StatelessResetTokenSize:], grant.Token[:]) {
				t.Fatalf("closed on a packet whose trailing bytes are not the live token")
			}
		}
		if d.Matched && !bytes.Equal(packet[len(packet)-StatelessResetTokenSize:], grant.Token[:]) {
			t.Fatal("matched packet does not carry the live token")
		}
	})
}
