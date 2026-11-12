package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestObservationInterruption 观察中断的两种形态：轮询间隔超窗、中间出现失败轮。
func TestObservationInterruption(t *testing.T) {
	f := newFixture(t)
	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)

	t.Run("gap beyond poll interval resets window", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		for hour := 0; hour < 10; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
		// 间隔 3 小时，超过 PollInterval(1h)+Skew(1m)。
		at := start.Add(13 * time.Hour)
		*clockPtr = at
		if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationGap) {
			t.Fatalf("want ErrObservationGap, got %v", err)
		}
		// 重新计时后 23 小时仍不足。
		for hour := 14; hour < 37; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("post-gap hour %d: %v", hour, err)
			}
		}
	})

	t.Run("failed poll resets window", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		for hour := 0; hour < 12; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
		// 第 12 小时出现一台服务器无签名的失败轮。
		at := start.Add(12 * time.Hour)
		*clockPtr = at
		bad := f.observation(t, at, "ns1", []DSRecord{good}, nil, f.ksk)
		bad.CDSSig = nil
		if _, err := tr.Observe(f.poll(at, bad,
			f.observation(t, at, "ns2", []DSRecord{good}, nil, f.ksk))); !errors.Is(err, ErrRRSIGUnsigned) {
			t.Fatalf("want ErrRRSIGUnsigned, got %v", err)
		}
		// 重新积累 23 小时仍不接纳。
		for hour := 13; hour < 36; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("post-failure hour %d: %v", hour, err)
			}
		}
	})

	t.Run("content change restarts the window", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		other := dsFor(t, f.zone, f.ksk.rec, DigestSHA256)
		for hour := 0; hour < 10; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
		// 换成另一套合法 CDS：指纹变化，观察从头计。
		at := start.Add(10 * time.Hour)
		*clockPtr = at
		if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{other})); !errors.Is(err, ErrObservationShort) {
			t.Fatalf("changed content should restart window: %v", err)
		}
	})

	t.Run("local clock skew beyond tolerance resets window", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		at := start
		*clockPtr = at
		if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
			t.Fatalf("first poll: %v", err)
		}
		// 轮询时间正常推进，但本地时钟漂移超过 1 分钟容差。
		at = start.Add(time.Hour)
		*clockPtr = at.Add(10 * time.Minute)
		if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationGap) {
			t.Fatalf("want ErrObservationGap for clock skew, got %v", err)
		}
	})
}

// TestCDSCDNSKEYCorrespondence 并存时二者须互相推导对应。
func TestCDSCDNSKEYCorrespondence(t *testing.T) {
	f := newFixture(t)

	t.Run("matching CDS and CDNSKEY accepted", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		cdns := []DNSKEYRecord{f.ksk2.rec}
		cds := []DSRecord{dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)}
		for hour := 0; hour < 25; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			poll := f.poll(at,
				f.observation(t, at, "ns1", cds, cdns, f.ksk),
				f.observation(t, at, "ns2", cds, cdns, f.ksk),
			)
			d, err := tr.Observe(poll)
			if hour == 24 {
				if err != nil || d.Action != ActionReplace {
					t.Fatalf("want replace, got action=%q err=%v", d.Action, err)
				}
			} else if !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
	})

	t.Run("CDS not derivable from CDNSKEY rejected", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		cdns := []DNSKEYRecord{f.ksk2.rec}
		// CDS 指向另一把钥，无法由 CDNSKEY 集推导。
		cds := []DSRecord{dsFor(t, f.zone, f.ksk.rec, DigestSHA256)}
		at := clock
		poll := f.poll(at,
			f.observation(t, at, "ns1", cds, cdns, f.ksk),
			f.observation(t, at, "ns2", cds, cdns, f.ksk),
		)
		if _, err := tr.Observe(poll); !errors.Is(err, ErrCDSCDNSKEYMismatch) {
			t.Fatalf("want ErrCDSCDNSKEYMismatch, got %v", err)
		}
	})

	t.Run("delete signal must be consistent across both rrtypes", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		cds := []DSRecord{{KeyTag: 0, Algorithm: 0, DigestType: 0, Digest: []byte{0}}}
		cdns := []DNSKEYRecord{f.ksk2.rec}
		at := clock
		poll := f.poll(at,
			f.observation(t, at, "ns1", cds, cdns, f.ksk),
			f.observation(t, at, "ns2", cds, cdns, f.ksk),
		)
		if _, err := tr.Observe(poll); !errors.Is(err, ErrCDSCDNSKEYMismatch) {
			t.Fatalf("want ErrCDSCDNSKEYMismatch, got %v", err)
		}
	})
}

// TestNewDSWithoutDNSKEY 新 DS 在已发布 DNSKEY 集中无对应钥时拒绝；
// 单独提供 CDNSKEY 时其内容也必须逐字节已发布。
func TestNewDSWithoutDNSKEY(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })

	stranger := genECDSA(t, AlgECDSAP256)
	strangerDS := dsFor(t, f.zone, stranger.rec, DigestSHA256)
	at := clock
	if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{strangerDS})); !errors.Is(err, ErrNewDSNoDNSKEY) {
		t.Fatalf("want ErrNewDSNoDNSKEY, got %v", err)
	}

	// CDNSKEY 含未发布钥同样拒绝。
	tr2 := newTestTracker(t, f, func() time.Time { return clock })
	poll := f.poll(at,
		f.observation(t, at, "ns1", nil, []DNSKEYRecord{stranger.rec}, f.ksk),
		f.observation(t, at, "ns2", nil, []DNSKEYRecord{stranger.rec}, f.ksk),
	)
	if _, err := tr2.Observe(poll); !errors.Is(err, ErrNewDSNoDNSKEY) {
		t.Fatalf("want ErrNewDSNoDNSKEY for unpublished CDNSKEY, got %v", err)
	}
}

// TestCDNSKEYOnlyDerivesDS 仅存在 CDNSKEY 时，成熟后由其推导 DS。
func TestCDNSKEYOnlyDerivesDS(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	clockPtr, clockFn := mutableClock(start)
	tr := newTestTracker(t, f, clockFn)
	cdns := []DNSKEYRecord{f.ksk2.rec}
	want := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)

	for hour := 0; hour < 25; hour++ {
		at := start.Add(time.Duration(hour) * time.Hour)
		*clockPtr = at
		poll := f.poll(at,
			f.observation(t, at, "ns1", nil, cdns, f.ksk),
			f.observation(t, at, "ns2", nil, cdns, f.ksk),
		)
		d, err := tr.Observe(poll)
		if hour == 24 {
			if err != nil || d.Action != ActionReplace {
				t.Fatalf("want replace, got action=%q err=%v", d.Action, err)
			}
			if len(d.NewDS) != 1 || !bytesEqual(dsRDATA(d.NewDS[0]), dsRDATA(want)) {
				t.Fatalf("derived DS mismatch: %+v", d.NewDS)
			}
		} else if !errors.Is(err, ErrObservationShort) {
			t.Fatalf("hour %d: %v", hour, err)
		}
	}
}

// TestSignatureAlgorithms RSA/SHA-256、ECDSA P-384、Ed25519 的签名链都应可用。
func TestSignatureAlgorithms(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T) testKey
	}{
		{"rsa-sha256", genRSA},
		{"ecdsa-p384", func(t *testing.T) testKey { return genECDSA(t, AlgECDSAP384) }},
		{"ed25519", genEd25519},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ksk := tc.make(t)
			clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
			tr, err := NewTracker(testConfig(func() time.Time { return clock }),
				[]DSRecord{dsFor(t, f.zone, ksk.rec, DigestSHA256)},
				[]Signer{signerOf(t, ksk)})
			if err != nil {
				t.Fatalf("NewTracker: %v", err)
			}
			cds := []DSRecord{dsFor(t, f.zone, ksk.rec, DigestSHA256)}
			at := clock
			poll := f.poll(at,
				f.observation(t, at, "ns1", cds, nil, ksk),
				f.observation(t, at, "ns2", cds, nil, ksk),
			)
			if _, err := tr.Observe(poll); !errors.Is(err, ErrObservationShort) {
				t.Fatalf("anchored %s signature should enter observation: %v", tc.name, err)
			}
		})
	}
}

// TestExpiredRRSIGRejected 过期 RRSIG 不得通过。
func TestExpiredRRSIGRejected(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })
	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)

	// 签名时刻早 3 小时，窗口 ±1h，对当前时刻已过期。
	old := clock.Add(-3 * time.Hour)
	stale := f.observation(t, old, "ns1", []DSRecord{good}, nil, f.ksk)
	poll := f.poll(clock, stale,
		f.observation(t, clock, "ns2", []DSRecord{good}, nil, f.ksk))
	if _, err := tr.Observe(poll); !errors.Is(err, ErrRRSIGTime) {
		t.Fatalf("want ErrRRSIGTime, got %v", err)
	}
}
