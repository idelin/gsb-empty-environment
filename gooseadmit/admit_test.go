package gooseadmit

import (
	"bytes"
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func berLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var b []byte
	for v := n; v > 0; v >>= 8 {
		b = append([]byte{byte(v & 0xff)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

func tlv(tag byte, val []byte) []byte {
	out := []byte{tag}
	out = append(out, berLength(len(val))...)
	return append(out, val...)
}

func berUint(v uint32) []byte {
	if v == 0 {
		return []byte{0x00}
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v & 0xff)}, b...)
		v >>= 8
	}
	return b
}

type frameOpts struct {
	src                   [6]byte
	gocbRef, datSet, goID string
	ttl, stNum, sqNum     uint32
	sim, vlan, wrongEther bool
}

func buildFrame(o frameOpts) []byte {
	var pdu bytes.Buffer
	pdu.Write(tlv(tagGoCBRef, []byte(o.gocbRef)))
	pdu.Write(tlv(tagTimeTTL, berUint(o.ttl)))
	pdu.Write(tlv(tagDataSet, []byte(o.datSet)))
	pdu.Write(tlv(tagGoID, []byte(o.goID)))
	pdu.Write(tlv(tagT, make([]byte, 8)))
	pdu.Write(tlv(tagStNum, berUint(o.stNum)))
	pdu.Write(tlv(tagSqNum, berUint(o.sqNum)))
	simVal := byte(0)
	if o.sim {
		simVal = 1
	}
	pdu.Write(tlv(tagSimulation, []byte{simVal}))
	pdu.Write(tlv(tagConfRev, berUint(1)))
	pdu.Write(tlv(tagNdsCom, []byte{0x00}))
	pdu.Write(tlv(tagNumDatSetEnt, berUint(1)))

	var frame bytes.Buffer
	frame.Write(make([]byte, 6)) // destination MAC
	frame.Write(o.src[:])
	if o.vlan {
		frame.Write([]byte{0x81, 0x00, 0x20, 0x00})
	}
	if o.wrongEther {
		frame.Write([]byte{0x08, 0x00})
	} else {
		frame.Write([]byte{0x88, 0xB8})
	}
	frame.Write(tlv(0x30, pdu.Bytes()))
	return frame.Bytes()
}

var testMAC = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

func testKey() PublisherKey {
	return NewPublisherKey(testMAC[:], "ied1/LLN0/GoCB1", "ied1/LLN0/DS1", "GO1")
}

func opts(key PublisherKey, stNum, sqNum uint32) frameOpts {
	return frameOpts{
		src:     key.SourceMAC,
		gocbRef: key.GoCBRef,
		datSet:  key.DatSet,
		goID:    key.GoID,
		ttl:     100,
		stNum:   stNum,
		sqNum:   sqNum,
	}
}

func newTestController(clk *fakeClock, testMode bool) *Controller {
	return New(&Config{TestMode: testMode, Now: clk.now})
}

func TestUnregisteredPublisherRejected(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)

	// An attacker forges a frame with a large stNum under an identity that was
	// never provisioned: it must be dropped before sequence logic runs.
	res := c.HandleFrame(buildFrame(opts(testKey(), 9999, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropUnregistered {
		t.Fatalf("got verdict=%v reason=%v, want reject/unregistered", res.Verdict, res.Reason)
	}

	key := testKey()
	c.Register(key)
	res = c.HandleFrame(buildFrame(opts(key, 1, 0)))
	if res.Verdict != VerdictNewEvent {
		t.Fatalf("first registered frame: got %v, want NewEvent", res.Verdict)
	}
	if !c.IsValid(key) {
		t.Fatal("registered publisher should be valid after first frame")
	}
}

func TestStNumJumpMustResetSqNum(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)

	if res := c.HandleFrame(buildFrame(opts(key, 1, 0))); res.Verdict != VerdictNewEvent {
		t.Fatalf("baseline: got %v", res.Verdict)
	}

	// stNum advances but sqNum was not reset to zero: forged or malformed.
	res := c.HandleFrame(buildFrame(opts(key, 2, 3)))
	if res.Verdict != VerdictReject || res.Reason != DropStNumWithoutSqZero {
		t.Fatalf("jump without sq reset: got verdict=%v reason=%v", res.Verdict, res.Reason)
	}

	// State is untouched: a legitimate retransmission of stNum=1 still works.
	if res := c.HandleFrame(buildFrame(opts(key, 1, 1))); res.Verdict != VerdictRetransmission {
		t.Fatalf("retransmission after rejected frame: got %v", res.Verdict)
	}

	if res := c.HandleFrame(buildFrame(opts(key, 2, 0))); res.Verdict != VerdictNewEvent {
		t.Fatalf("proper event: got %v, want NewEvent", res.Verdict)
	}
}

func TestRetransmissionDuplicateAndReplay(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)
	c.HandleFrame(buildFrame(opts(key, 1, 0)))

	if res := c.HandleFrame(buildFrame(opts(key, 1, 1))); res.Verdict != VerdictRetransmission {
		t.Fatalf("sq 1: got %v, want Retransmission", res.Verdict)
	}

	// Exact duplicate is ignored.
	if res := c.HandleFrame(buildFrame(opts(key, 1, 1))); res.Verdict != VerdictDuplicate {
		t.Fatalf("duplicate: got %v, want Duplicate", res.Verdict)
	}

	// Older sqNum replayed from a capture.
	res := c.HandleFrame(buildFrame(opts(key, 1, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropSqReplay {
		t.Fatalf("sq replay: got verdict=%v reason=%v", res.Verdict, res.Reason)
	}

	// Older stNum is also a replay.
	res = c.HandleFrame(buildFrame(opts(key, 0, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropStNumDecreased {
		t.Fatalf("stNum decrease: got verdict=%v reason=%v", res.Verdict, res.Reason)
	}
}

func TestSqNumWraparound(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)

	// Drive the publisher to the end of the 32-bit sqNum space.
	state := c.pubs[key]
	state.initialized = true
	state.lastStNum = 42
	state.lastSqNum = ^uint32(0)
	state.ttl = time.Second
	state.lastSeen = clk.t

	// Wrap from 2^32-1 to zero is the only legal backwards step.
	if res := c.HandleFrame(buildFrame(opts(key, 42, 0))); res.Verdict != VerdictRetransmission {
		t.Fatalf("legal wrap: got %v, want Retransmission", res.Verdict)
	}
	if got := c.StatusOf(key).LastSqNum; got != 0 {
		t.Fatalf("sqNum after wrap = %d, want 0", got)
	}

	// A jump to zero from any other position is a replay, not a wrap.
	state.lastSqNum = 5
	res := c.HandleFrame(buildFrame(opts(key, 42, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropSqReplay {
		t.Fatalf("fake wrap: got verdict=%v reason=%v", res.Verdict, res.Reason)
	}
}

func TestStNumDecreaseRequiresRestartAnnouncement(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)
	c.HandleFrame(buildFrame(opts(key, 5, 0)))

	res := c.HandleFrame(buildFrame(opts(key, 1, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropStNumDecreased {
		t.Fatalf("decrease without notice: got %v/%v", res.Verdict, res.Reason)
	}

	if !c.NotifyRestart(key) {
		t.Fatal("NotifyRestart should succeed for a registered key")
	}
	if c.NotifyRestart(PublisherKey{GoID: "ghost"}) {
		t.Fatal("NotifyRestart should fail for an unknown key")
	}

	// Rebooted publisher restarts numbering from scratch.
	if res := c.HandleFrame(buildFrame(opts(key, 1, 0))); res.Verdict != VerdictNewEvent {
		t.Fatalf("post-restart frame: got %v, want NewEvent", res.Verdict)
	}

	// The restart arm is one-shot: decreasing again is rejected.
	res = c.HandleFrame(buildFrame(opts(key, 0, 0)))
	if res.Verdict != VerdictReject || res.Reason != DropStNumDecreased {
		t.Fatalf("arm not consumed: got %v/%v", res.Verdict, res.Reason)
	}
}

func TestTimeAllowedToLiveExpiry(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)
	c.HandleFrame(buildFrame(opts(key, 1, 0))) // ttl = 100 ms

	clk.advance(99 * time.Millisecond)
	if !c.IsValid(key) {
		t.Fatal("should still be valid at 99 ms")
	}
	if expired := c.Sweep(); len(expired) != 0 {
		t.Fatalf("no expiry expected, got %v", expired)
	}

	// A duplicate must not extend liveness: lastSeen stays at t0.
	clk.advance(1 * time.Millisecond)
	if res := c.HandleFrame(buildFrame(opts(key, 1, 0))); res.Verdict != VerdictDuplicate {
		t.Fatalf("duplicate: got %v", res.Verdict)
	}

	clk.advance(1 * time.Millisecond) // 101 ms since the last accepted frame
	if c.IsValid(key) {
		t.Fatal("data should be invalid after timeAllowedToLive")
	}
	expired := c.Sweep()
	if len(expired) != 1 || expired[0] != key {
		t.Fatalf("Sweep = %v, want %v", expired, key)
	}
	if expired := c.Sweep(); len(expired) != 0 {
		t.Fatalf("second Sweep should not re-report, got %v", expired)
	}

	// A replayed/duplicate frame cannot resurrect expired data.
	res := c.HandleFrame(buildFrame(opts(key, 1, 0)))
	if res.Verdict != VerdictDuplicate {
		t.Fatalf("stale frame while expired: got %v", res.Verdict)
	}
	if c.IsValid(key) {
		t.Fatal("duplicate must not clear the expired flag")
	}

	// A genuinely fresh retransmission revives the publisher.
	if res := c.HandleFrame(buildFrame(opts(key, 1, 1))); res.Verdict != VerdictRetransmission {
		t.Fatalf("fresh frame after expiry: got %v", res.Verdict)
	}
	if !c.IsValid(key) {
		t.Fatal("fresh accepted frame should restore validity")
	}
}

func TestSimulationBit(t *testing.T) {
	clk := newFakeClock()
	key := testKey()

	// Production subscriber rejects simulated GOOSE.
	prod := newTestController(clk, false)
	prod.Register(key)
	o := opts(key, 1, 0)
	o.sim = true
	if res := prod.HandleFrame(buildFrame(o)); res.Verdict != VerdictReject || res.Reason != DropSimulation {
		t.Fatalf("prod sim frame: got %v/%v", res.Verdict, res.Reason)
	}
	o.sim = false
	if res := prod.HandleFrame(buildFrame(o)); res.Verdict != VerdictNewEvent {
		t.Fatalf("prod real frame: got %v", res.Verdict)
	}

	// Test subscriber accepts the simulated stream.
	testC := newTestController(clk, true)
	testC.Register(key)
	if res := testC.HandleFrame(buildFrame(o)); res.Verdict != VerdictNewEvent {
		t.Fatalf("test-mode baseline: got %v", res.Verdict)
	}
	o.sim = true
	o.sqNum = 1
	if res := testC.HandleFrame(buildFrame(o)); res.Verdict != VerdictRetransmission {
		t.Fatalf("test-mode sim frame: got %v", res.Verdict)
	}

	// Flipping the subscriber back to production cuts the simulated stream.
	testC.SetTestMode(false)
	o.sqNum = 2
	if res := testC.HandleFrame(buildFrame(o)); res.Verdict != VerdictReject || res.Reason != DropSimulation {
		t.Fatalf("runtime mode switch: got %v/%v", res.Verdict, res.Reason)
	}
}

func TestEtherTypeVLANAndMalformed(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)

	// Non-GOOSE EtherType is ignored rather than parsed.
	wrong := opts(key, 1, 0)
	wrong.wrongEther = true
	if res := c.HandleFrame(buildFrame(wrong)); res.Reason != DropNotGOOSE {
		t.Fatalf("wrong ethertype: got %v, want DropNotGOOSE", res.Reason)
	}

	// 802.1Q-tagged GOOSE frames parse normally.
	tagged := opts(key, 1, 0)
	tagged.vlan = true
	if res := c.HandleFrame(buildFrame(tagged)); res.Verdict != VerdictNewEvent {
		t.Fatalf("tagged frame: got %v, want NewEvent", res.Reason)
	}

	// Truncated and empty input never panic.
	if res := c.HandleFrame(nil); res.Reason != DropMalformed {
		t.Fatalf("nil frame: got %v, want DropMalformed", res.Reason)
	}
	if res := c.HandleFrame(make([]byte, 6)); res.Reason != DropMalformed {
		t.Fatalf("short frame: got %v, want DropMalformed", res.Reason)
	}
	if res := c.HandleFrame(buildFrame(opts(key, 1, 0))[:16]); res.Reason != DropMalformed {
		t.Fatalf("truncated frame: got %v, want DropMalformed", res.Reason)
	}
}

func TestPublisherIdentityTuple(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)

	base := opts(key, 1, 0)
	if res := c.HandleFrame(buildFrame(base)); res.Verdict != VerdictNewEvent {
		t.Fatalf("baseline: got %v", res.Verdict)
	}

	// Same goID but spoofed source MAC: a different tuple, not registered.
	spoofMAC := base
	spoofMAC.src = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	res := c.HandleFrame(buildFrame(spoofMAC))
	if res.Verdict != VerdictReject || res.Reason != DropUnregistered {
		t.Fatalf("spoofed MAC: got %v/%v", res.Verdict, res.Reason)
	}

	// Tampered gocbRef likewise fails the four-tuple lookup.
	tampered := base
	tampered.gocbRef = "ied2/LLN0/GoCB1"
	res = c.HandleFrame(buildFrame(tampered))
	if res.Verdict != VerdictReject || res.Reason != DropUnregistered {
		t.Fatalf("tampered gocbRef: got %v/%v", res.Verdict, res.Reason)
	}
}

func TestForgedLargeStNum(t *testing.T) {
	clk := newFakeClock()
	c := newTestController(clk, false)
	key := testKey()
	c.Register(key)
	c.HandleFrame(buildFrame(opts(key, 1, 0)))

	// A valid-looking event with a huge stNum is only accepted if it follows
	// the protocol rule sqNum == 0. A non-zero sqNum exposes the forgery.
	if res := c.HandleFrame(buildFrame(opts(key, 4_000_000_000, 7))); res.Reason != DropStNumWithoutSqZero {
		t.Fatalf("forged jump with stale sq: got %v", res.Reason)
	}

	// Sequence baseline stays anchored at stNum=1, so the retransmit below
	// remains valid and the attack changed nothing.
	if res := c.HandleFrame(buildFrame(opts(key, 1, 1))); res.Verdict != VerdictRetransmission {
		t.Fatalf("baseline retransmit: got %v", res.Verdict)
	}

	// A legitimately advanced event with sqNum reset is accepted.
	if res := c.HandleFrame(buildFrame(opts(key, 2, 0))); res.Verdict != VerdictNewEvent {
		t.Fatalf("legit event: got %v", res.Verdict)
	}
}
