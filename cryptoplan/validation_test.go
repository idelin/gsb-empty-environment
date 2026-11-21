package cryptoplan

import (
	"errors"
	"testing"
	"time"
)

func TestInputValidation(t *testing.T) {
	host := newTestHost(t)
	nb, na := minuteTime(0)
	validCert := host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
		[]string{algMLKEM}, nb, na)
	validEdge := host.edge(t, "e1", "a1@example.test", "p1", "", nb, na)

	t.Run("nil snapshot", func(t *testing.T) {
		_, err := PlanDelivery(nil, &DeliveryRequest{}, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("too many groups", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups:              make([]DeviceGroup, MaxDeviceGroups+1),
			AlgorithmPreference: preference(),
			ReleaseTime:         time.Now(),
		}
		_, err := PlanDelivery(snapshot(host, nil, nil), req, nil)
		if !errors.Is(err, ErrTooManyGroups) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty preference", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups:      []DeviceGroup{{StableID: "g1"}},
			ReleaseTime: time.Now(),
		}
		_, err := PlanDelivery(snapshot(host, nil, nil), req, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate preference", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups:              []DeviceGroup{{StableID: "g1"}},
			AlgorithmPreference: []string{algMLKEM, algMLKEM},
			ReleaseTime:         time.Now(),
		}
		_, err := PlanDelivery(snapshot(host, nil, nil), req, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("missing host key", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups:              []DeviceGroup{{StableID: "g1"}},
			AlgorithmPreference: preference(),
			ReleaseTime:         time.Now(),
		}
		_, err := PlanDelivery(&Snapshot{}, req, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate stable id", func(t *testing.T) {
		snap := snapshot(host, []DeviceCert{validCert}, []AliasEdge{validEdge})
		req := &DeliveryRequest{
			Groups: []DeviceGroup{
				{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
				{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			},
			AlgorithmPreference: preference(),
			ReleaseTime:         time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC),
		}
		_, err := PlanDelivery(snap, req, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestSignatureFailures(t *testing.T) {
	host := newTestHost(t)
	nb, na := minuteTime(0)
	cert := host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
		[]string{algMLKEM}, nb, na)
	edge := host.edge(t, "e1", "a1@example.test", "p1", "", nb, na)

	t.Run("tampered cert signature", func(t *testing.T) {
		rogue := cert
		rogue.Signature = append([]byte(nil), cert.Signature...)
		rogue.Signature[0] ^= 0xFF
		err := VerifyDeviceCert(rogue, host.pub)
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tampered edge signature", func(t *testing.T) {
		rogue := edge
		rogue.Signature = append([]byte(nil), edge.Signature...)
		rogue.Signature[0] ^= 0xFF
		err := VerifyAliasEdge(rogue, host.pub)
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cert key id mismatch", func(t *testing.T) {
		rogue := cert
		rogue.KeyID = "wrong-key-id"
		err := VerifyDeviceCert(rogue, host.pub)
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad host key length", func(t *testing.T) {
		if err := VerifyDeviceCert(cert, nil); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("got %v", err)
		}
		if err := VerifyAliasEdge(edge, nil); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid cert shape", func(t *testing.T) {
		bad := cert
		bad.CertID = ""
		if err := VerifyDeviceCert(bad, host.pub); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
		badEdge := edge
		badEdge.EdgeID = ""
		if err := VerifyAliasEdge(badEdge, host.pub); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestNoFeasiblePlan(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	cert := host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
		[]string{algRSA}, nb, na)
	edge := host.edge(t, "e1", "a1@example.test", "p1", "", nb, na)
	snap := snapshot(host, []DeviceCert{cert}, []AliasEdge{edge})

	t.Run("no preferred algorithm authorized", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups: []DeviceGroup{
				{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			},
			AlgorithmPreference: []string{algMLKEM},
			ReleaseTime:         release,
		}
		if _, err := PlanDelivery(snap, req, nil); !errors.Is(err, ErrNoFeasiblePlan) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("expired alias edge", func(t *testing.T) {
		req := &DeliveryRequest{
			Groups: []DeviceGroup{
				{StableID: "g1", Recipient: "unknown@example.test",
					DeviceIDs: []string{"d1"}},
			},
			AlgorithmPreference: preference(),
			ReleaseTime:         release,
		}
		if _, err := PlanDelivery(snap, req, nil); !errors.Is(err, ErrNoFeasiblePlan) {
			t.Fatalf("got %v", err)
		}
	})
}
