package netsec

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

var testHostKey = []byte("0123456789abcdef0123456789abcdef")

func newTestCache(t *testing.T, cfg Config) *Cache {
	t.Helper()
	if cfg.HostKey == nil {
		cfg.HostKey = append([]byte(nil), testHostKey...)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func testRequest(tenant string, tokens ...Token) Request {
	return Request{
		TenantID:               tenant,
		ModelDigest:            "model:weights:fp16",
		AdapterDigest:          "",
		TokenizerDigest:        "tokenizer:v1",
		Position:               0,
		Tokens:                 append([]Token(nil), tokens...),
		SafetyPolicyGeneration: 1,
		KVLayoutVersion:        1,
	}
}

func testSegments(layers int, start, end int64, label string) []SegmentData {
	out := make([]SegmentData, 0, layers)
	for layer := 0; layer < layers; layer++ {
		out = append(out, SegmentData{
			Layer:      layer,
			TokenStart: start,
			TokenEnd:   end,
			K:          []byte(fmt.Sprintf("%s-k-%d", label, layer)),
			V:          []byte(fmt.Sprintf("%s-v-%d", label, layer)),
		})
	}
	return out
}

func mustCreate(t *testing.T, c *Cache, req Request, segments []SegmentData) *Lease {
	t.Helper()
	lease, err := c.Create(t.Context(), req, segments)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return lease
}

func mustLookup(t *testing.T, c *Cache, req Request) *Lease {
	t.Helper()
	lease, ok, err := c.Lookup(t.Context(), req)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok {
		t.Fatalf("Lookup missed for %#v", req)
	}
	return lease
}

func mustMiss(t *testing.T, c *Cache, req Request) {
	t.Helper()
	lease, ok, err := c.Lookup(t.Context(), req)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if ok {
		lease.Release()
		t.Fatalf("unexpected hit for %#v", req)
	}
}

func promptHash(label string) [32]byte {
	return sha256.Sum256([]byte(label))
}

func mustAllSlotsScrubbed(t *testing.T, p *Pool) {
	t.Helper()
	for i := 0; i < p.Size(); i++ {
		info, err := p.SlotInfo(uint32(i))
		if err != nil {
			t.Fatalf("SlotInfo(%d): %v", i, err)
		}
		if info.State != SlotFree {
			t.Fatalf("slot %d state = %v, want free", i, info.State)
		}
		if !info.AllZero {
			t.Fatalf("slot %d still contains non-zero bytes", i)
		}
	}
}
