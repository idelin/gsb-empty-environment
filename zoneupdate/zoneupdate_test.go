package zoneupdate

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
)

var (
	recWWW  = Record{Name: "www", RType: "A", Value: "192.0.2.1"}
	recMail = Record{Name: "mail", RType: "A", Value: "192.0.2.25"}
	recFTP  = Record{Name: "ftp", RType: "A", Value: "192.0.2.21"}
	recAPI  = Record{Name: "api", RType: "A", Value: "192.0.2.80"}
)

func masterKey(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func newService(t *testing.T, zone string, key ed25519.PrivateKey, initial ...Record) *Service {
	t.Helper()
	s := NewService()
	if err := s.RegisterZone(zone, key.Public().(ed25519.PublicKey), initial); err != nil {
		t.Fatalf("RegisterZone: %v", err)
	}
	return s
}

func cloneSeg(s *Segment) *Segment {
	c := *s
	c.Ops = append([]Op(nil), s.Ops...)
	c.Signature = append([]byte(nil), s.Signature...)
	return &c
}

func cloneRound(segs []*Segment) []*Segment {
	out := make([]*Segment, len(segs))
	for i, s := range segs {
		out[i] = cloneSeg(s)
	}
	return out
}

func mustVersion(t *testing.T, s *Service, zone string) uint64 {
	t.Helper()
	v, err := s.Version(zone)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	return v
}

func mustRecords(t *testing.T, s *Service, zone string) []Record {
	t.Helper()
	recs, err := s.Records(zone)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	return recs
}

// assertZoneIntact fails the test unless the zone is exactly at wantVer
// holding exactly wantRecs.
func assertZoneIntact(t *testing.T, s *Service, zone string, wantVer uint64, wantRecs ...Record) {
	t.Helper()
	if got := mustVersion(t, s, zone); got != wantVer {
		t.Errorf("version = %d, want %d", got, wantVer)
	}
	got := mustRecords(t, s, zone)
	if len(got) != len(wantRecs) {
		t.Fatalf("records = %v, want %v", got, wantRecs)
	}
	for i, r := range wantRecs {
		if got[i] != r {
			t.Errorf("record %d = %+v, want %+v", i, got[i], r)
		}
	}
}

func TestValidRoundAccepted(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW, recMail)

	segs := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		AddSegment(Delete(recMail), Add(recAPI)).
		Build()

	ver, err := svc.Submit("example.com", segs)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ver != 2 {
		t.Fatalf("version = %d, want 2", ver)
	}
	assertZoneIntact(t, svc, "example.com", 2, recAPI, recFTP, recWWW)
}

func TestCrossZoneSpliceRejected(t *testing.T) {
	// Both zones trust the same master key, so the spliced segment is
	// validly signed; only the bound zone identity can reject it.
	key := masterKey(1)
	svc := newService(t, "a.example", key, recWWW, recMail)
	if err := svc.RegisterZone("b.example", key.Public().(ed25519.PublicKey),
		[]Record{recFTP}); err != nil {
		t.Fatalf("RegisterZone b.example: %v", err)
	}

	// A legitimately signed round for b.example deleting recFTP.
	victim := NewBuilder("b.example", 0, key).
		AddSegment(Delete(recFTP)).
		Build()

	// Attacker replaces the final segment of a.example's round with the
	// foreign delete segment.
	spliced := NewBuilder("a.example", 0, key).
		AddSegment(Add(recAPI)).
		AddSegment(Add(recFTP)).
		Build()
	spliced[1] = cloneSeg(victim[0])

	if _, err := svc.Submit("a.example", spliced); !errors.Is(err, ErrWrongZone) {
		t.Fatalf("Submit err = %v, want ErrWrongZone", err)
	}
	assertZoneIntact(t, svc, "a.example", 0, recMail, recWWW)
	assertZoneIntact(t, svc, "b.example", 0, recFTP)
}

func TestCrossRoundSpliceRejected(t *testing.T) {
	// A delete segment from another round of the same zone, signed by the
	// legitimate master, must not be accepted in the current round.
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW, recMail)

	// Round 2, built on version 1, deletes recMail.
	round2 := NewBuilder("example.com", 1, key).
		AddSegment(Delete(recMail)).
		Build()

	// Round 1 adds recFTP then recAPI; attacker swaps in round 2's
	// delete segment as the final segment.
	spliced := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		AddSegment(Add(recAPI)).
		Build()
	spliced[1] = cloneSeg(round2[0])

	if _, err := svc.Submit("example.com", spliced); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("Submit err = %v, want ErrBrokenChain", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recMail, recWWW)
}

func TestTruncatedChainRejected(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW, recMail)

	full := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		AddSegment(Delete(recMail)).
		AddSegment(Add(recAPI)).
		Build()

	// Drop the final segment: the round commitment is missing.
	if _, err := svc.Submit("example.com", cloneRound(full[:2])); !errors.Is(err, ErrBadFinal) {
		t.Fatalf("truncated tail err = %v, want ErrBadFinal", err)
	}
	// Drop a middle segment: the chain linkage breaks.
	cut := []*Segment{cloneSeg(full[0]), cloneSeg(full[2])}
	if _, err := svc.Submit("example.com", cut); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("missing middle err = %v, want ErrBrokenChain", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recMail, recWWW)
}

func TestDuplicateSequenceRejected(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	full := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		AddSegment(Delete(recWWW)).
		Build()

	// Replay segment 1 in place of segment 2.
	dup := []*Segment{cloneSeg(full[0]), cloneSeg(full[0])}
	if _, err := svc.Submit("example.com", dup); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("Submit err = %v, want ErrBrokenChain", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recWWW)
}

func TestDeleteMustMatchOldRecord(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	// The delete digest matches no record; the add in the earlier
	// segment must not be published either.
	segs := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		AddSegment(Delete(recMail)).
		Build()

	if _, err := svc.Submit("example.com", segs); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("Submit err = %v, want ErrUnknownRecord", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recWWW)
}

func TestTamperedSignatureRejected(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	segs := NewBuilder("example.com", 0, key).
		AddSegment(Delete(recWWW)).
		Build()
	segs[0].Signature[0] ^= 0xff

	if _, err := svc.Submit("example.com", segs); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit err = %v, want ErrBadSignature", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recWWW)
}

func TestForgedFinalMetadataRejected(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	// Inflate the segment count after signing: the signature no longer
	// matches the payload.
	segs := NewBuilder("example.com", 0, key).
		AddSegment(Delete(recWWW)).
		Build()
	segs[0].SegCount = 99

	if _, err := svc.Submit("example.com", segs); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit err = %v, want ErrBadSignature", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recWWW)
}

func TestWrongKeyRejected(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	attacker := masterKey(2)
	segs := NewBuilder("example.com", 0, attacker).
		AddSegment(Delete(recWWW)).
		Build()

	if _, err := svc.Submit("example.com", segs); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit err = %v, want ErrBadSignature", err)
	}
	assertZoneIntact(t, svc, "example.com", 0, recWWW)
}

func TestConcurrentRoundsExactlyOneCommits(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW, recMail)

	// Two rounds built on the same base version race: one deletes
	// recMail, the other deletes recWWW. Exactly one may commit; the
	// loser must fail without deleting anything.
	rounds := [][]*Segment{
		NewBuilder("example.com", 0, key).
			AddSegment(Delete(recMail)).
			Build(),
		NewBuilder("example.com", 0, key).
			AddSegment(Delete(recWWW)).
			Build(),
	}

	var wg sync.WaitGroup
	results := make([]error, len(rounds))
	for i, segs := range rounds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = svc.Submit("example.com", segs)
		}()
	}
	wg.Wait()

	committed := 0
	for _, err := range results {
		switch {
		case err == nil:
			committed++
		case errors.Is(err, ErrVersionMismatch):
			// Expected loser: the winner already moved the version.
		default:
			t.Fatalf("unexpected Submit err = %v", err)
		}
	}
	if committed != 1 {
		t.Fatalf("committed rounds = %d, want 1", committed)
	}
	if got := mustVersion(t, svc, "example.com"); got != 1 {
		t.Fatalf("version = %d, want 1", got)
	}
	// Exactly one record was deleted by the winner; the loser's delete
	// was not applied.
	recs := mustRecords(t, svc, "example.com")
	if len(recs) != 1 || (recs[0] != recWWW && recs[0] != recMail) {
		t.Fatalf("records = %v, want exactly one of www/mail", recs)
	}
}

func TestSequentialRoundsAccepted(t *testing.T) {
	key := masterKey(1)
	svc := newService(t, "example.com", key, recWWW)

	round1 := NewBuilder("example.com", 0, key).
		AddSegment(Add(recFTP)).
		Build()
	if _, err := svc.Submit("example.com", round1); err != nil {
		t.Fatalf("round 1: %v", err)
	}

	// A round anchored at the new current version is accepted.
	round2 := NewBuilder("example.com", 1, key).
		AddSegment(Delete(recWWW)).
		Build()
	if _, err := svc.Submit("example.com", round2); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	assertZoneIntact(t, svc, "example.com", 2, recFTP)

	// Replaying round 1 is stale and rejected.
	if _, err := svc.Submit("example.com", cloneRound(round1)); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("replay err = %v, want ErrVersionMismatch", err)
	}
	assertZoneIntact(t, svc, "example.com", 2, recFTP)
}
