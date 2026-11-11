package vlan

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSwapIsolation hammers the datapath from many goroutines while
// another goroutine swaps policies. Invariants checked on every frame:
//
//   - a frame is processed under exactly one snapshot (its tenant matches the
//     canonical stack it produced),
//   - every accepted egress rewrite restores exactly the original wire bytes
//     on a port that the effective snapshot admits for that tenant,
//   - no frame ever re-emerges carrying another tenant's VID.
func TestConcurrentSwapIsolation(t *testing.T) {
	policyA := testPolicy(t)
	policyB, err := NewPolicy([]PortPolicy{
		{Name: "acc1", Mode: ModeAccess, AccessVID: 10},
		{Name: "acc2", Mode: ModeAccess, AccessVID: 20},
		{
			Name:      "trk",
			Mode:      ModeTrunk,
			NativeVID: 99,
			Allowed:   []uint16{10}, // snapshot B removes VIDs 20 and 100
		},
		{Name: "tun", Mode: ModeTunnel, AccessVID: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	sw := NewSwitch(policyA)

	type flow struct {
		port string
		tags []Tag
	}
	payload := []byte{0x08, 0x00, 0xDE, 0xAD, 0xBE, 0xEF}
	flows := []flow{
		{"acc1", nil},
		{"acc2", nil},
		{"trk", []Tag{tag(EtherTypeVLAN, 0, false, 100)}},
		{"trk", []Tag{tag(EtherTypeVLAN, 0, false, 20)}},
		{"tun", []Tag{tag(EtherTypeVLAN, 0, false, 33)}},
	}

	// Build a distinct wire frame per flow so cross-flow contamination would
	// be detectable byte-for-byte.
	wireFrames := make([][]byte, len(flows))
	for i, fl := range flows {
		p := make([]byte, len(payload))
		copy(p, payload)
		p[len(p)-1] = byte(i + 1)
		wireFrames[i] = buildFrame(t, fl.tags, p)
	}

	var stop atomic.Bool
	var swaps atomic.Int64
	var wg sync.WaitGroup
	started := make(chan struct{})
	proceed := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		for i := 0; i < 500 && !stop.Load(); i++ {
			sw.Swap(policyB)
			swaps.Add(1)
			sw.Swap(policyA)
			swaps.Add(1)
			if i == 0 {
				close(proceed)
			}
			runtime.Gosched()
		}
	}()
	<-started
	<-proceed // first swap pair has landed before workers read the switch

	var failures atomic.Int64
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for round := 0; round < 2000; round++ {
				idx := (id + round) % len(flows)
				fl := flows[idx]
				raw := wireFrames[idx]

				canonical, tenant, inErr := sw.Ingress(fl.port, raw)
				if inErr != nil {
					// Policy B may legitimately refuse some flows; only
					// parse/drop errors consistent with a snapshot are OK.
					if !errors.Is(inErr, ErrIngressDenied) &&
						!errors.Is(inErr, ErrTruncated) &&
						!errors.Is(inErr, ErrTagStackTooDeep) {
						failures.Add(1)
						t.Errorf("unexpected ingress error: %v", inErr)
						return
					}
					continue
				}

				// Tenant must be derivable from the canonical stack itself.
				parsed, perr := ParseFrame(canonical)
				if perr != nil || tenantOf(parsed.Tags) != tenant {
					failures.Add(1)
					t.Errorf("tenant %v inconsistent with stack %v (parse err %v)",
						tenant, parsed.Tags, perr)
					return
				}

				out, eerr := sw.Egress(fl.port, tenant, canonical)
				if eerr != nil {
					if !errors.Is(eerr, ErrEgressDenied) &&
						!errors.Is(eerr, ErrTenantMismatch) {
						failures.Add(1)
						t.Errorf("unexpected egress error: %v", eerr)
						return
					}
					continue
				}

				// A successful egress must reproduce the exact ingress wire
				// bytes: any VID from another tenant would show up here.
				if !bytes.Equal(out, raw) {
					failures.Add(1)
					t.Errorf("frame leaked/rewritten on port %s:\n got %x\nwant %x",
						fl.port, out, raw)
					return
				}
			}
		}(worker)
	}

	stop.Store(true)
	wg.Wait()

	if swaps.Load() == 0 {
		t.Fatal("no policy swaps observed")
	}
	if failures.Load() != 0 {
		t.Fatalf("%d isolation failures under concurrent swaps", failures.Load())
	}
}

// TestSwapNeverServesMixedSnapshot verifies that after a swap settles, frames
// admitted only by the old snapshot are refused and frames moved by the new
// one get the new mapping.
func TestSwapNeverServesMixedSnapshot(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	payload := []byte{0x08, 0x00, 0x77}

	// Under policy A, VID 100 is allowed on trk and the tunnel service is 500.
	oldTagged := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 100)}, payload)
	if _, _, err := sw.Ingress("trk", oldTagged); err != nil {
		t.Fatalf("policy A should allow VID 100: %v", err)
	}

	sw.Swap(mustPolicy(t, []PortPolicy{
		{Name: "acc1", Mode: ModeAccess, AccessVID: 10},
		{Name: "trk", Mode: ModeTrunk, Allowed: []uint16{10}},
		{Name: "tun", Mode: ModeTunnel, AccessVID: 600},
	}))

	if _, _, err := sw.Ingress("trk", oldTagged); !errors.Is(err, ErrIngressDenied) {
		t.Fatalf("policy B must refuse VID 100, got %v", err)
	}

	canonical, tenant, err := sw.Ingress("tun", buildFrame(t, nil, payload))
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFrame(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if f.Tags[0].VID != 600 {
		t.Fatalf("service VID = %d, want 600 from new snapshot", f.Tags[0].VID)
	}
	if tenant.InnerVID != 600 {
		t.Fatalf("tenant = %v", tenant)
	}
}

func mustPolicy(t *testing.T, ports []PortPolicy) *Policy {
	t.Helper()
	p, err := NewPolicy(ports)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
