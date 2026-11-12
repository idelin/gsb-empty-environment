package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestCDNSKEYDeleteAndMixed 覆盖 CDNSKEY 删除信号的并存一致性与混放拒绝。
func TestCDNSKEYDeleteAndMixed(t *testing.T) {
	f := newFixture(t)

	t.Run("canonical CDNSKEY delete accepted", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		del := []DNSKEYRecord{{Flags: 0, Protocol: 3, Algorithm: 0, PublicKey: []byte{0}}}
		for hour := 0; hour < 25; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			poll := f.poll(at,
				f.observation(t, at, "ns1", nil, del, f.ksk),
				f.observation(t, at, "ns2", nil, del, f.ksk),
			)
			d, err := tr.Observe(poll)
			if hour == 24 {
				if err != nil || d.Action != ActionDelete {
					t.Fatalf("want delete, got action=%q err=%v", d.Action, err)
				}
			} else if !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
	})

	t.Run("CDNSKEY delete mixed with normal key rejected", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		mixed := []DNSKEYRecord{
			{Flags: 0, Protocol: 3, Algorithm: 0, PublicKey: []byte{0}},
			f.ksk2.rec,
		}
		at := clock
		poll := f.poll(at,
			f.observation(t, at, "ns1", nil, mixed, f.ksk),
			f.observation(t, at, "ns2", nil, mixed, f.ksk),
		)
		if _, err := tr.Observe(poll); !errors.Is(err, ErrDeleteMixed) {
			t.Fatalf("want ErrDeleteMixed, got %v", err)
		}
	})

	t.Run("bad DNSKEY protocol rejected", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		bad := []DNSKEYRecord{{
			Flags: 257, Protocol: 99, Algorithm: AlgECDSAP256, PublicKey: f.ksk2.rec.PublicKey,
		}}
		at := clock
		poll := f.poll(at,
			f.observation(t, at, "ns1", nil, bad, f.ksk),
			f.observation(t, at, "ns2", nil, bad, f.ksk),
		)
		if _, err := tr.Observe(poll); !errors.Is(err, ErrMalformed) {
			t.Fatalf("want ErrMalformed, got %v", err)
		}
	})
}

// TestInvalidConfig 构造期配置校验。
func TestInvalidConfig(t *testing.T) {
	f := newFixture(t)
	signers := f.signers(t)
	ds := f.currentDS(t)

	if _, err := NewTracker(Config{RequireHours: 0}, ds, signers); !errors.Is(err, ErrConfig) {
		t.Fatalf("zero RequireHours: %v", err)
	}
	if _, err := NewTracker(Config{RequireHours: 1}, nil, signers); !errors.Is(err, ErrConfig) {
		t.Fatalf("empty current DS: %v", err)
	}
	if _, err := NewTracker(Config{RequireHours: 1}, ds, nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("empty signers: %v", err)
	}
}

// TestMalformedPoll 观察报文结构校验。
func TestMalformedPoll(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })

	if _, err := tr.Observe(Poll{Zone: f.zone, At: clock}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty servers: %v", err)
	}
	at := clock
	empty := f.observation(t, at, "ns1", nil, nil, f.ksk)
	if _, err := tr.Observe(f.poll(at, empty)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("no CDS/CDNSKEY: %v", err)
	}
}

// TestKeyTagAndDigest RFC 4034 附录 B 与 RFC 4509 摘要的独立向量校验。
func TestKeyTagAndDigest(t *testing.T) {
	k := genECDSA(t, AlgECDSAP256).rec
	tag := KeyTag(k)
	d256, err := DSDigest(testZone, k, DigestSHA256)
	if err != nil || len(d256) != 32 {
		t.Fatalf("sha256 digest: %v len=%d", err, len(d256))
	}
	d384, err := DSDigest(testZone, k, DigestSHA384)
	if err != nil || len(d384) != 48 {
		t.Fatalf("sha384 digest: %v len=%d", err, len(d384))
	}
	ds := DSRecord{KeyTag: tag, Algorithm: k.Algorithm, DigestType: DigestSHA256, Digest: d256}
	if !DSMatchesDNSKEY(ds, testZone, k) {
		t.Fatal("DS should match its source DNSKEY")
	}
	if DSMatchesDNSKEY(ds, "other.test.", k) {
		t.Fatal("DS must not match under a different owner name")
	}
	if _, err := DSDigest(testZone, k, 99); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown digest type: %v", err)
	}
}
