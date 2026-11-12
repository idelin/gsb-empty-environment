package netsec

import (
	"errors"
	"testing"
)

var (
	peerMAC     = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	attackerMAC = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x99}

	ki0  = KeyID("ki-sak-0")
	ki1  = KeyID("ki-sak-1")
	sak0 = SAK{Index: 10, Key: []byte("zero-zero-zero-zero-zero-sak-0")}
	sak1 = SAK{Index: 11, Key: []byte("zero-zero-zero-zero-zero-sak-1")}
)

func newPeerSCI(t *testing.T, port PortNumber) SCI {
	t.Helper()
	sci, err := NewSCI(peerMAC, port)
	if err != nil {
		t.Fatalf("NewSCI: %v", err)
	}
	return sci
}

func newAttackerSCI(t *testing.T, port PortNumber) SCI {
	t.Helper()
	sci, err := NewSCI(attackerMAC, port)
	if err != nil {
		t.Fatalf("NewSCI: %v", err)
	}
	return sci
}

// Table 1: a SecTAG-selected channel must be both MKA-advertised and bound to
// a KI/SAK. A forged SCI is rejected regardless of the packet it carries.
func TestForgedSCIRejected(t *testing.T) {
	table := NewRxTable()
	peer := newPeerSCI(t, 1)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	if _, err := table.InstallSA(peer, 0, ki0, sak0); err != nil {
		t.Fatalf("InstallSA: %v", err)
	}

	tests := []struct {
		name string
		sci  SCI
		an   uint8
		want error
	}{
		{"unknown source MAC", newAttackerSCI(t, 1), 0, ErrUnknownSCI},
		{"known MAC, unadvertised port", newPeerSCI(t, 2), 0, ErrUnknownSCI},
		{"out of range AN", peer, 4, ErrInvalidAN},
		{"advertised SCI, unbound AN", peer, 1, ErrNoSecureAssociation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := table.Lookup(tc.sci, tc.an); !errors.Is(err, tc.want) {
				t.Fatalf("Lookup(%s) = %v, want %v", tc.name, err, tc.want)
			}
			if err := table.Receive(SecTag{SCI: tc.sci, AssociationNumber: tc.an, PacketNumber: 1}, true); !errors.Is(err, tc.want) {
				t.Fatalf("Receive(%s) = %v, want %v", tc.name, err, tc.want)
			}
		})
	}

	if table.ChannelCount() != 1 {
		t.Fatalf("ChannelCount = %d, want 1", table.ChannelCount())
	}
	if table.Learned(newAttackerSCI(t, 1)) {
		t.Fatal("forged SCI must never be learned implicitly")
	}
}

// Installing an SA on an unadvertised SCI is itself forbidden, so a control
// plane cannot accidentally open a channel MKA never authorized.
func TestInstallRequiresAdvertisedSCI(t *testing.T) {
	table := NewRxTable()
	if _, err := table.InstallSA(newAttackerSCI(t, 1), 0, ki0, sak0); !errors.Is(err, ErrUnknownSCI) {
		t.Fatalf("InstallSA on unknown SCI = %v, want ErrUnknownSCI", err)
	}
}

// A frame must present the exact KI MKA bound to the selected association.
// Replaying an old KI, or any other identifier, is rejected before decaps.
func TestStaleKeyIdentifierRejected(t *testing.T) {
	table := NewRxTable()
	peer := newPeerSCI(t, 1)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	sa, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}

	if err := sa.VerifyKeyID(ki0); err != nil {
		t.Fatalf("VerifyKeyID(bound KI) = %v, want nil", err)
	}
	if err := sa.VerifyKeyID(ki1); !errors.Is(err, ErrStaleSAK) {
		t.Fatalf("VerifyKeyID(other KI) = %v, want ErrStaleSAK", err)
	}
	if err := sa.VerifyKeyID(KeyID("forged-ki")); !errors.Is(err, ErrStaleSAK) {
		t.Fatalf("VerifyKeyID(forged KI) = %v, want ErrStaleSAK", err)
	}
}

// After MKA retires an SAK, frames still selecting its AN must be refused.
// This covers the "old SAK" requirement independently of a live rekey.
func TestRetiredSAKRejected(t *testing.T) {
	table := NewRxTable()
	peer := newPeerSCI(t, 1)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	old, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}

	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 1}, true); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if old.HighestPN() != 1 {
		t.Fatalf("HighestPN = %d, want 1", old.HighestPN())
	}

	if err := table.RetireSA(peer, 0); err != nil {
		t.Fatalf("RetireSA: %v", err)
	}
	if _, err := table.Lookup(peer, 0); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("Lookup after retire = %v, want ErrRetiredSAK", err)
	}
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 2}, true); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("Receive on retired SA = %v, want ErrRetiredSAK", err)
	}

	// A retired slot may be re-provisioned with a fresh SAK after rekey.
	if _, err := table.InstallSA(peer, 0, ki1, sak1); err != nil {
		t.Fatalf("reprovision retired slot: %v", err)
	}
	sa, err := table.Lookup(peer, 0)
	if err != nil {
		t.Fatalf("Lookup reprovisioned: %v", err)
	}
	if sa.BoundKeyID() != ki1 {
		t.Fatalf("BoundKeyID = %q, want %q", sa.BoundKeyID(), ki1)
	}
}

// The packet-number window only advances for authenticated frames. Duplicates,
// non-incrementing numbers and frames beyond the window are dropped, and a
// failed ICV leaves the window untouched.
func TestReplayWindow(t *testing.T) {
	const win = 4
	table := NewRxTable(WithReplayWindow(win))
	peer := newPeerSCI(t, 1)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	sa, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}
	if sa.Window() != win {
		t.Fatalf("Window = %d, want %d", sa.Window(), win)
	}

	accept := func(pn uint32) {
		t.Helper()
		if err := sa.CheckPN(pn); err != nil {
			t.Fatalf("CheckPN(%d): %v", pn, err)
		}
		if err := sa.Commit(pn); err != nil {
			t.Fatalf("Commit(%d): %v", pn, err)
		}
	}
	reject := func(pn uint32) {
		t.Helper()
		if err := sa.CheckPN(pn); !errors.Is(err, ErrReplay) {
			t.Fatalf("CheckPN(%d) = %v, want ErrReplay", pn, err)
		}
	}

	// Packet number zero is reserved and never valid.
	if err := sa.CheckPN(0); !errors.Is(err, ErrInvalidPacketNumber) {
		t.Fatalf("CheckPN(0) = %v, want ErrInvalidPacketNumber", err)
	}

	accept(1)
	accept(2)
	accept(3)

	// Non-incrementing (equal to highest) and older duplicates are dropped.
	reject(3)
	reject(2)
	reject(1)

	// In-order arrival continues normally; highest is now 6.
	accept(4)
	accept(5)
	accept(6)

	// Already-seen numbers are duplicates even though still inside the window.
	reject(5)
	reject(3)

	// A gap jump lets us exercise the window edge without bitmap collisions.
	accept(20)                             // highest is now 20; window retains offsets 1..3 (PN 19..17)
	if err := sa.CheckPN(17); err != nil { // offset 3: boundary inside window
		t.Fatalf("CheckPN(17) in window: %v", err)
	}
	accept(17) // late but inside the window; recorded
	reject(17) // second arrival is now a duplicate
	reject(16) // offset 4 >= window 4: outside the window
	reject(2)  // far outside the window

	// A jump beyond the bitmap width resets history without false duplicates.
	accept(100)
	reject(6)
	if got := sa.HighestPN(); got != 100 {
		t.Fatalf("HighestPN = %d, want 100", got)
	}

	// Failed ICV must not move the window.
	before := sa.HighestPN()
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 101}, false); !errors.Is(err, ErrICV) {
		t.Fatalf("Receive(bad ICV) = %v, want ErrICV", err)
	}
	if sa.HighestPN() != before {
		t.Fatalf("ICV failure advanced window: %d -> %d", before, sa.HighestPN())
	}
	// The same PN is still acceptable once a valid ICV is presented.
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 101}, true); err != nil {
		t.Fatalf("Receive(good ICV after prior failure): %v", err)
	}
	if sa.HighestPN() != 101 {
		t.Fatalf("HighestPN = %d, want 101", sa.HighestPN())
	}
}

// Wrap-around of the 32-bit packet number must keep the signed-distance window
// logic coherent instead of treating a wrapped high value as ancient.
func TestReplayWindowWrapAround(t *testing.T) {
	table := NewRxTable(WithReplayWindow(4))
	peer := newPeerSCI(t, 1)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	sa, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}

	if err := sa.Commit(^uint32(0)); err != nil { // PN 0xFFFFFFFF
		t.Fatalf("Commit max PN: %v", err)
	}
	if err := sa.Commit(1); err != nil { // wraps to 1
		t.Fatalf("Commit wrapped PN: %v", err)
	}
	if sa.HighestPN() != 1 {
		t.Fatalf("HighestPN = %d, want 1", sa.HighestPN())
	}
	// Pre-wrap max PN is now two behind (offset 2), in-window but duplicate.
	if err := sa.CheckPN(^uint32(0)); !errors.Is(err, ErrReplay) {
		t.Fatalf("CheckPN(pre-wrap) = %v, want ErrReplay", err)
	}
}

// A legal make-before-break rekey keeps both associations live during the
// switch: traffic flows on the old AN, the new KI/SAK is installed on another
// AN, in-flight frames on either AN pass, and only after MKA retires the old
// AN are its frames refused while the new AN continues.
func TestLegitimateRekeySwitch(t *testing.T) {
	table := NewRxTable()
	peer := newPeerSCI(t, 7)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	oldSA, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA old: %v", err)
	}

	// Traffic on the original association, PNs 1..2.
	for _, pn := range []uint32{1, 2} {
		if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: pn}, true); err != nil {
			t.Fatalf("old-SA PN %d: %v", pn, err)
		}
	}

	newSA, err := table.Rekey(peer, 0, 1, ki1, sak1)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if newSA.BoundKeyID() != ki1 || newSA.AssociationNumber() != 1 {
		t.Fatalf("new SA = AN %d KI %q", newSA.AssociationNumber(), newSA.BoundKeyID())
	}

	// During overlap both ANs select distinct, active associations.
	gotOld, err := table.Lookup(peer, 0)
	if err != nil {
		t.Fatalf("Lookup old AN during overlap: %v", err)
	}
	gotNew, err := table.Lookup(peer, 1)
	if err != nil {
		t.Fatalf("Lookup new AN during overlap: %v", err)
	}
	if gotOld == gotNew {
		t.Fatal("rekey must not replace the old association during overlap")
	}
	if err := gotNew.VerifyKeyID(ki1); err != nil {
		t.Fatalf("new SA KI: %v", err)
	}

	// In-flight frame on old AN still passes; new AN starts its own PN space.
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 3}, true); err != nil {
		t.Fatalf("old AN in-flight frame: %v", err)
	}
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 1, PacketNumber: 1}, true); err != nil {
		t.Fatalf("new AN first frame: %v", err)
	}

	// Reinstalling onto an active new AN is rejected.
	if _, err := table.Rekey(peer, 0, 1, ki1, sak1); !errors.Is(err, ErrAssociationExists) {
		t.Fatalf("Rekey onto active AN = %v, want ErrAssociationExists", err)
	}

	// MKA completes the switch: retire the old key; old frames now fail while
	// new-key traffic continues with its independent replay window.
	if err := table.RetireSA(peer, 0); err != nil {
		t.Fatalf("RetireSA: %v", err)
	}
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 4}, true); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("old AN after retire = %v, want ErrRetiredSAK", err)
	}
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 1, PacketNumber: 2}, true); err != nil {
		t.Fatalf("new AN second frame: %v", err)
	}
	if oldSA.HighestPN() != 3 {
		t.Fatalf("old SA HighestPN = %d, want 3", oldSA.HighestPN())
	}
	if newSA.HighestPN() != 2 {
		t.Fatalf("new SA HighestPN = %d, want 2", newSA.HighestPN())
	}

	// A stale KI presented against the new association is refused.
	if err := gotNew.VerifyKeyID(ki0); !errors.Is(err, ErrStaleSAK) {
		t.Fatalf("new SA accepting old KI = %v, want ErrStaleSAK", err)
	}
}

func TestSCIEncodeDecode(t *testing.T) {
	sci, err := NewSCI([]byte{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}, 0x1234)
	if err != nil {
		t.Fatalf("NewSCI: %v", err)
	}
	want := []byte{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x12, 0x34}

	encoded, err := sci.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if len(encoded) != len(want) {
		t.Fatalf("encoded length = %d, want %d", len(encoded), len(want))
	}
	for i := range want {
		if encoded[i] != want[i] {
			t.Fatalf("encoded[%d] = %#x, want %#x", i, encoded[i], want[i])
		}
	}

	var decoded SCI
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if decoded != sci {
		t.Fatalf("round trip = %+v, want %+v", decoded, sci)
	}
	if got := sci.String(); got != "0a:0b:0c:0d:0e:0f#1234" {
		t.Fatalf("String = %q", got)
	}

	if _, err := DecodeSCI(make([]byte, 7)); !errors.Is(err, ErrInvalidSCI) {
		t.Fatalf("DecodeSCI(short) = %v, want ErrInvalidSCI", err)
	}
	if _, err := NewSCI([]byte{1, 2, 3}, 1); !errors.Is(err, ErrInvalidMAC) {
		t.Fatalf("NewSCI(short MAC) = %v, want ErrInvalidMAC", err)
	}
}

// MustSCI constructs valid constants and panics on malformed input; Encode
// rejects undersized destination buffers.
func TestSCIMustAndEncodeErrors(t *testing.T) {
	sci := MustSCI(peerMAC, 42)
	if sci.Port != 42 {
		t.Fatalf("Port = %d, want 42", sci.Port)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("MustSCI with bad MAC must panic")
			}
		}()
		_ = MustSCI([]byte{1, 2}, 1)
	}()

	if _, err := sci.Encode(make([]byte, 7)); !errors.Is(err, ErrBufferTooShort) {
		t.Fatalf("Encode(short) = %v, want ErrBufferTooShort", err)
	}
}

// Covers the remaining table and association lifecycle branches.
func TestLifecycleEdgeCases(t *testing.T) {
	// An out-of-range window option is ignored and the default retained.
	table := NewRxTable(WithReplayWindow(0), WithReplayWindow(65), WithReplayWindow(8))
	peer := newPeerSCI(t, 3)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	if err := table.LearnSCI(peer); err != nil { // idempotent
		t.Fatalf("re-LearnSCI = %v, want nil", err)
	}

	sa, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}
	if sa.SCI() != peer || sa.SAK().Index != sak0.Index || string(sa.SAK().Key) != string(sak0.Key) {
		t.Fatal("accessor mismatch for SCI/SAK")
	}
	if sa.Window() != 8 {
		t.Fatalf("Window = %d, want 8", sa.Window())
	}

	// Active AN cannot be overwritten; invalid AN rejected.
	if _, err := table.InstallSA(peer, 0, ki1, sak1); !errors.Is(err, ErrAssociationExists) {
		t.Fatalf("overwrite active = %v, want ErrAssociationExists", err)
	}
	if _, err := table.InstallSA(peer, 9, ki1, sak1); !errors.Is(err, ErrInvalidAN) {
		t.Fatalf("InstallSA bad AN = %v, want ErrInvalidAN", err)
	}

	// Rekey guard clauses.
	if _, err := table.Rekey(peer, 0, 0, ki1, sak1); !errors.Is(err, ErrInvalidAN) {
		t.Fatalf("Rekey same AN = %v, want ErrInvalidAN", err)
	}
	if _, err := table.Rekey(peer, 2, 1, ki1, sak1); !errors.Is(err, ErrNoSecureAssociation) {
		t.Fatalf("Rekey missing old = %v, want ErrNoSecureAssociation", err)
	}
	if _, err := table.Rekey(newAttackerSCI(t, 3), 0, 1, ki1, sak1); !errors.Is(err, ErrUnknownSCI) {
		t.Fatalf("Rekey unknown SCI = %v, want ErrUnknownSCI", err)
	}

	// Retire guard clauses and double retirement.
	if err := table.RetireSA(peer, 9); !errors.Is(err, ErrInvalidAN) {
		t.Fatalf("RetireSA bad AN = %v, want ErrInvalidAN", err)
	}
	if err := table.RetireSA(peer, 2); !errors.Is(err, ErrNoSecureAssociation) {
		t.Fatalf("RetireSA empty = %v, want ErrNoSecureAssociation", err)
	}
	if err := table.RetireSA(newAttackerSCI(t, 3), 0); !errors.Is(err, ErrUnknownSCI) {
		t.Fatalf("RetireSA unknown SCI = %v, want ErrUnknownSCI", err)
	}
	if err := table.RetireSA(peer, 0); err != nil {
		t.Fatalf("RetireSA: %v", err)
	}
	if err := table.RetireSA(peer, 0); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("double retire = %v, want ErrRetiredSAK", err)
	}

	// Rekey refusing to build on a retired old association.
	if _, err := table.Rekey(peer, 0, 1, ki1, sak1); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("Rekey from retired = %v, want ErrRetiredSAK", err)
	}

	// ForgetSCI tears the channel down; subsequent operations see unknown SCI.
	if err := table.ForgetSCI(peer); err != nil {
		t.Fatalf("ForgetSCI: %v", err)
	}
	if err := table.ForgetSCI(peer); !errors.Is(err, ErrUnknownSCI) {
		t.Fatalf("double ForgetSCI = %v, want ErrUnknownSCI", err)
	}
	if table.Learned(peer) {
		t.Fatal("Learned must be false after ForgetSCI")
	}
}

// Direct association calls cover reserved-PN, retirement and equal-PN commit
// paths that the table-level flow never reaches.
func TestRxSADirectBranches(t *testing.T) {
	table := NewRxTable(WithReplayWindow(4))
	peer := newPeerSCI(t, 5)
	if err := table.LearnSCI(peer); err != nil {
		t.Fatalf("LearnSCI: %v", err)
	}
	sa, err := table.InstallSA(peer, 0, ki0, sak0)
	if err != nil {
		t.Fatalf("InstallSA: %v", err)
	}

	if err := sa.Commit(0); !errors.Is(err, ErrInvalidPacketNumber) {
		t.Fatalf("Commit(0) = %v, want ErrInvalidPacketNumber", err)
	}

	// Establish highestPN = 10, then exercise the equal and late commit arms.
	if err := sa.Commit(10); err != nil {
		t.Fatalf("Commit(10): %v", err)
	}
	if err := sa.Commit(10); err != nil { // diff == 0, no-op arm
		t.Fatalf("Commit(10) again: %v", err)
	}
	if err := sa.Commit(8); err != nil { // diff < 0, in-window bitmap arm
		t.Fatalf("Commit(8) late: %v", err)
	}
	// The late number is now recorded and rejected on re-check.
	if err := sa.CheckPN(8); !errors.Is(err, ErrReplay) {
		t.Fatalf("CheckPN(8) after commit = %v, want ErrReplay", err)
	}

	// Receive must surface a replay rejection from the CheckPN step.
	if err := table.Receive(SecTag{SCI: peer, AssociationNumber: 0, PacketNumber: 8}, true); !errors.Is(err, ErrReplay) {
		t.Fatalf("Receive duplicate = %v, want ErrReplay", err)
	}

	// After retirement both the check and commit refuse further processing.
	if err := sa.retire(); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if err := sa.CheckPN(11); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("CheckPN retired = %v, want ErrRetiredSAK", err)
	}
	if err := sa.Commit(11); !errors.Is(err, ErrRetiredSAK) {
		t.Fatalf("Commit retired = %v, want ErrRetiredSAK", err)
	}
}
