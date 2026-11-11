package netsec

import (
	"errors"
	"strings"
	"testing"
)

// entryMapper 模拟受信入口映射：仅识别入口层 metadata 中已知隧道对。
func entryMapper(meta map[string]string) (string, bool) {
	if meta["tunnel"] == "gre:edge-1" {
		return "tenant-a", true
	}
	return "", false
}

func staticLayer(r Range, meta map[string]string) LayerDecoder {
	return func(payload []byte) (Layer, error) {
		return Layer{Payload: r, Metadata: meta}, nil
	}
}

// slicingLayer 返回一个按父载荷比例收缩的合法区间，用于构造多层事件。
func slicingLayer(meta map[string]string) LayerDecoder {
	return func(payload []byte) (Layer, error) {
		if len(payload) < 2 {
			return Layer{Payload: Range{0, len(payload)}, Metadata: meta}, nil
		}
		return Layer{Payload: Range{1, len(payload) - 1}, Metadata: meta}, nil
	}
}

func TestDecodeAssignsIdentityOnlyFromTrustedEntry(t *testing.T) {
	d := NewDecoder(
		Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20},
		entryMapper,
		staticLayer(Range{0, 64}, map[string]string{"tunnel": "gre:edge-1"}),
		slicingLayer(map[string]string{TenantClaimKey: "tenant-a", "inner_proto": "vxlan"}),
		slicingLayer(nil),
	)
	ev, err := d.Decode(make([]byte, 128))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if ev.TenantID != "tenant-a" {
		t.Fatalf("TenantID = %q, want tenant-a", ev.TenantID)
	}
	if len(ev.Layers) != 3 {
		t.Fatalf("len(Layers) = %d, want 3", len(ev.Layers))
	}
	if got := ev.Layers[1].Metadata[TenantClaimKey]; got != "tenant-a" {
		t.Fatalf("evidence tenant claim = %q, want tenant-a", got)
	}
	if ev.DecodedBytes != int64(128+64+62) {
		t.Fatalf("DecodedBytes = %d, want %d", ev.DecodedBytes, 128+64+62)
	}
}

func TestInnerTenantClaimNeverBecomesIdentity(t *testing.T) {
	d := NewDecoder(
		Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20},
		entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "unknown"}),
		slicingLayer(map[string]string{TenantClaimKey: "tenant-b"}),
	)
	ev, err := d.Decode(make([]byte, 64))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if ev.TenantID != "" {
		t.Fatalf("TenantID = %q, want empty (inner claim must not be trusted)", ev.TenantID)
	}
	if got := ev.Layers[1].Metadata[TenantClaimKey]; got != "tenant-b" {
		t.Fatalf("inner claim should remain as evidence, got %q", got)
	}
}

func TestOutOfBoundsRangeRejectedAndDecoderUnpolluted(t *testing.T) {
	malicious := NewDecoder(Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20}, entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "gre:edge-1"}),
		staticLayer(Range{10, 10_000}, nil),
	)
	if _, err := malicious.Decode(make([]byte, 64)); !errors.Is(err, ErrRangeOutOfBounds) {
		t.Fatalf("Decode err = %v, want ErrRangeOutOfBounds", err)
	}
	good := NewDecoder(Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20}, entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "gre:edge-1"}),
		slicingLayer(nil),
	)
	ev, err := good.Decode(make([]byte, 64))
	if err != nil {
		t.Fatalf("subsequent Decode: %v", err)
	}
	if ev.TenantID != "tenant-a" {
		t.Fatalf("subsequent TenantID = %q, want tenant-a", ev.TenantID)
	}
}

func TestMalformedRangesRejected(t *testing.T) {
	cases := map[string]Range{
		"negative start":    {-1, 8},
		"end before start":  {8, 4},
		"end beyond parent": {0, 65},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			d := NewDecoder(Limits{MaxDepth: 2, MaxDecodedBytes: 1 << 20}, nil,
				staticLayer(Range{0, 32}, nil),
				staticLayer(r, nil),
			)
			if _, err := d.Decode(make([]byte, 64)); !errors.Is(err, ErrRangeOutOfBounds) {
				t.Fatalf("Decode err = %v, want ErrRangeOutOfBounds", err)
			}
		})
	}
}

func TestNestedIdentityConflictRejectedAndDecoderUnpolluted(t *testing.T) {
	d := NewDecoder(
		Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20},
		entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "gre:edge-1"}),
		slicingLayer(map[string]string{TenantClaimKey: "tenant-b"}),
	)
	if _, err := d.Decode(make([]byte, 64)); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("Decode err = %v, want ErrIdentityConflict", err)
	}
	clean := NewDecoder(
		Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20},
		entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "gre:edge-1"}),
		slicingLayer(map[string]string{TenantClaimKey: "tenant-a"}),
	)
	ev, err := clean.Decode(make([]byte, 64))
	if err != nil {
		t.Fatalf("subsequent Decode: %v", err)
	}
	if ev.TenantID != "tenant-a" {
		t.Fatalf("subsequent TenantID = %q, want tenant-a", ev.TenantID)
	}
}

func TestDepthLimitEnforced(t *testing.T) {
	d := NewDecoder(Limits{MaxDepth: 1, MaxDecodedBytes: 1 << 20}, nil,
		slicingLayer(nil),
		slicingLayer(nil),
	)
	if _, err := d.Decode(make([]byte, 64)); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("Decode err = %v, want ErrDepthLimit", err)
	}
}

func TestCumulativeByteLimitEnforced(t *testing.T) {
	d := NewDecoder(Limits{MaxDepth: 8, MaxDecodedBytes: 130}, nil,
		slicingLayer(nil),
		slicingLayer(nil),
		slicingLayer(nil),
	)
	if _, err := d.Decode(make([]byte, 64)); !errors.Is(err, ErrByteLimit) {
		t.Fatalf("Decode err = %v, want ErrByteLimit", err)
	}
}

func TestDecoderErrorPropagates(t *testing.T) {
	boom := errors.New("corrupt header")
	d := NewDecoder(Limits{MaxDepth: 2, MaxDecodedBytes: 1 << 20}, nil,
		func([]byte) (Layer, error) { return Layer{}, boom },
	)
	if _, err := d.Decode(make([]byte, 16)); !errors.Is(err, boom) {
		t.Fatalf("Decode err = %v, want wrapped %v", err, boom)
	}
}

func TestMetadataDefensivelyCopied(t *testing.T) {
	shared := map[string]string{"tunnel": "gre:edge-1"}
	var mutate func()
	entry := func(payload []byte) (Layer, error) {
		return Layer{Payload: Range{0, 32}, Metadata: shared}, nil
	}
	inner := func(payload []byte) (Layer, error) {
		mutate()
		return Layer{Payload: Range{0, len(payload)}, Metadata: nil}, nil
	}
	d := NewDecoder(Limits{MaxDepth: 4, MaxDecodedBytes: 1 << 20}, entryMapper, entry, inner)
	mutate = func() { shared["tunnel"] = "forged" }
	ev, err := d.Decode(make([]byte, 64))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := ev.Layers[0].Metadata["tunnel"]; got != "gre:edge-1" {
		t.Fatalf("recorded entry metadata = %q, want gre:edge-1 (must be copied)", got)
	}
	if ev.TenantID != "tenant-a" {
		t.Fatalf("TenantID = %q, want tenant-a", ev.TenantID)
	}
}

func TestErrorMessagesAreInformative(t *testing.T) {
	d := NewDecoder(Limits{MaxDepth: 2, MaxDecodedBytes: 1 << 20}, entryMapper,
		staticLayer(Range{0, 32}, map[string]string{"tunnel": "gre:edge-1"}),
		staticLayer(Range{0, 999}, nil),
	)
	_, err := d.Decode(make([]byte, 64))
	if err == nil || !strings.Contains(err.Error(), "depth 1") {
		t.Fatalf("error should mention offending depth, got %v", err)
	}
}
