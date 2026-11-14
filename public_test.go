package netsec_test

import (
	"testing"
	"time"

	netsec "example.org/netsec/q0181"
)

func TestImportedPackageSynthesizesAndCaches(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	apex := netsec.MustParseName("example.")
	qname := netsec.MustParseName("ghost.example.")
	sig := func(covered uint16) netsec.RRSIG {
		return netsec.RRSIG{
			TypeCovered:   covered,
			Algorithm:     8,
			KeyTag:        1,
			Signer:        apex,
			KeyGeneration: 3,
			ValidFrom:     now.Add(-time.Hour),
			ValidUntil:    now.Add(2 * time.Hour),
			Verified:      true,
		}
	}
	resp := netsec.Response{
		Attestation: netsec.Attestation{
			View:               "tenant-a",
			Apex:               apex,
			Class:              netsec.ClassIN,
			KeyGeneration:      3,
			PolicyGeneration:   7,
			Parameters:         netsec.ParamsNSEC(),
			ChainVerified:      true,
			SignaturesVerified: true,
			ValidFrom:          now.Add(-time.Hour),
			ValidUntil:         now.Add(2 * time.Hour),
		},
		Question: netsec.Question{Name: qname, Type: netsec.TypeA, Class: netsec.ClassIN},
		NSEC: []netsec.NSECRecord{
			{
				Owner: netsec.MustParseName("g.example."),
				Next:  netsec.MustParseName("h.example."),
				Types: netsec.MustTypeBitmap(netsec.TypeA, netsec.TypeRRSIG, netsec.TypeNSEC),
				Class: netsec.ClassIN,
				TTL:   300,
				RRSIG: sig(netsec.TypeNSEC),
			},
			{
				Owner: apex,
				Next:  netsec.MustParseName("a.example."),
				Types: netsec.MustTypeBitmap(netsec.TypeSOA, netsec.TypeNS, netsec.TypeRRSIG, netsec.TypeNSEC),
				Class: netsec.ClassIN,
				TTL:   300,
				RRSIG: sig(netsec.TypeNSEC),
			},
		},
	}
	cache := netsec.NewCache(netsec.ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(netsec.Policy{
		View:               "tenant-a",
		Generation:         7,
		KeyGeneration:      3,
		MaxNSEC3Iterations: netsec.DefaultNSEC3MaxIterations,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Ingest(resp); err != nil {
		t.Fatal(err)
	}
	answer, ok := cache.Lookup(netsec.Query{
		View:  "tenant-a",
		Name:  qname,
		Type:  netsec.TypeAAAA,
		Class: netsec.ClassIN,
	})
	if !ok || !answer.CacheHit || answer.Result != netsec.ResultNXDOMAIN {
		t.Fatalf("imported API result = %+v, %v", answer, ok)
	}
}
