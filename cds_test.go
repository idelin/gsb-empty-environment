package netsec

import (
	"errors"
	"testing"
	"time"
)

func newTestTracker(t *testing.T, f *fixture, clock func() time.Time) *Tracker {
	t.Helper()
	tr, err := NewTracker(testConfig(clock), f.currentDS(t), f.signers(t))
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tr
}

// consistentPolls 让两台权威服务器在每轮返回同样的 CDS（由 f.ksk 签名）。
func consistentPolls(t *testing.T, f *fixture, at time.Time, cds []DSRecord) Poll {
	t.Helper()
	return f.poll(at,
		f.observation(t, at, "ns1", cds, nil, f.ksk),
		f.observation(t, at, "ns2", cds, nil, f.ksk),
	)
}

// TestHappyPathAccept 基线：所有服务器一致、由当前 DS 锚定 KSK 签名、
// 连续观察满 24 小时后接纳替换。
func TestHappyPathAccept(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	clockPtr, now := mutableClock(start)
	tr := newTestTracker(t, f, now)

	newDS := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)
	var last *Decision
	for hour := 0; hour < 25; hour++ {
		at := start.Add(time.Duration(hour) * time.Hour)
		*clockPtr = at
		d, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{newDS}))
		if hour < 24 {
			if !errors.Is(err, ErrObservationShort) || d.Action != ActionPending {
				t.Fatalf("hour %d: want pending/ErrObservationShort, got action=%q err=%v", hour, d.Action, err)
			}
		} else {
			if err != nil || d.Action != ActionReplace {
				t.Fatalf("hour %d: want replace, got action=%q err=%v", hour, d.Action, err)
			}
			if len(d.NewDS) != 1 || !bytesEqual(dsRDATA(d.NewDS[0]), dsRDATA(newDS)) {
				t.Fatalf("decision NewDS mismatch: %+v", d.NewDS)
			}
			if d.ObservedFor < 24*time.Hour {
				t.Fatalf("observed duration too short: %v", d.ObservedFor)
			}
		}
		last = d
	}
	if last.Fingerprint == "" {
		t.Fatal("fingerprint should be populated while pending")
	}
}

// TestSingleServerDivergence 单台权威服务器返回不同 CDS 集：立即拒绝且
// 连续观察状态被清空，之后即使恢复一致也必须重新计时。
func TestSingleServerDivergence(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	clockPtr, now := mutableClock(start)
	tr := newTestTracker(t, f, now)

	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)
	evil := dsFor(t, f.zone, f.zsk.rec, DigestSHA256)

	for hour := 0; hour < 10; hour++ {
		at := start.Add(time.Duration(hour) * time.Hour)
		*clockPtr = at
		if _, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good})); !errors.Is(err, ErrObservationShort) {
			t.Fatalf("hour %d: %v", hour, err)
		}
	}

	at := start.Add(10 * time.Hour)
	*clockPtr = at
	badPoll := f.poll(at,
		f.observation(t, at, "ns1", []DSRecord{good}, nil, f.ksk),
		f.observation(t, at, "ns2", []DSRecord{evil}, nil, f.ksk),
	)
	d, err := tr.Observe(badPoll)
	if !errors.Is(err, ErrServerDivergence) || d.Action != ActionPending {
		t.Fatalf("want ErrServerDivergence, got action=%q err=%v", d.Action, err)
	}

	// 状态已清空：再观察 23 个一致小时仍不应接纳。
	for hour := 11; hour < 35; hour++ {
		at := start.Add(time.Duration(hour) * time.Hour)
		*clockPtr = at
		d, err := tr.Observe(consistentPolls(t, f, at, []DSRecord{good}))
		if !errors.Is(err, ErrObservationShort) {
			t.Fatalf("post-divergence hour %d: want restarted window, got action=%q err=%v",
				hour, d.Action, err)
		}
	}
	// 第 24 个恢复小时（hour 34，即分歧后的第 24 轮）才成熟。
	at = start.Add(35 * time.Hour)
	*clockPtr = at
	d, err = tr.Observe(consistentPolls(t, f, at, []DSRecord{good}))
	if err != nil || d.Action != ActionReplace {
		t.Fatalf("want replace after full restarted window, got action=%q err=%v", d.Action, err)
	}
}

// TestSignerNotAnchored CDS 由另一把 KSK（不被当前 DS 指向）签名时拒绝。
func TestSignerNotAnchored(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })

	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)
	at := clock
	poll := f.poll(at,
		f.observation(t, at, "ns1", []DSRecord{good}, nil, f.ksk2),
		f.observation(t, at, "ns2", []DSRecord{good}, nil, f.ksk2),
	)
	d, err := tr.Observe(poll)
	if !errors.Is(err, ErrSignerNotAnchored) || d.Action != ActionPending {
		t.Fatalf("want ErrSignerNotAnchored, got action=%q err=%v", d.Action, err)
	}

	// 同样的记录换回当前 DS 锚定的 KSK 签名即合法（进入观察期）。
	d, err = tr.Observe(consistentPolls(t, f, at, []DSRecord{good}))
	if !errors.Is(err, ErrObservationShort) {
		t.Fatalf("anchored signature should enter observation, got action=%q err=%v", d.Action, err)
	}
}

// TestSignerNotKSK 由未置 SEP 位的 ZSK 签名时拒绝。
func TestSignerNotKSK(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })

	// 先给 ZSK 挂上当前 DS（模拟错误配置的父区），以区分“非 KSK”与“未锚定”。
	tr.currentDS = append(tr.currentDS, dsFor(t, f.zone, f.zsk.rec, DigestSHA256))

	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)
	at := clock
	poll := f.poll(at,
		f.observation(t, at, "ns1", []DSRecord{good}, nil, f.zsk),
		f.observation(t, at, "ns2", []DSRecord{good}, nil, f.zsk),
	)
	if _, err := tr.Observe(poll); !errors.Is(err, ErrSignerNotKSK) {
		t.Fatalf("want ErrSignerNotKSK, got %v", err)
	}
}

// TestTamperedSignature 篡改 RRSIG 后密码学校验失败。
func TestTamperedSignature(t *testing.T) {
	f := newFixture(t)
	clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tr := newTestTracker(t, f, func() time.Time { return clock })

	good := dsFor(t, f.zone, f.ksk2.rec, DigestSHA256)
	at := clock
	obs := f.observation(t, at, "ns1", []DSRecord{good}, nil, f.ksk)
	// 翻转 DER 末段（S 整数内容）的一个字节，保持 ASN.1 结构完整但使签名失效。
	obs.CDSSig.Signature[len(obs.CDSSig.Signature)-2] ^= 0x01
	_, err := tr.Observe(f.poll(at, obs,
		f.observation(t, at, "ns2", []DSRecord{good}, nil, f.ksk)))
	if !errors.Is(err, ErrRRSIGInvalid) {
		t.Fatalf("want ErrRRSIGInvalid, got %v", err)
	}
}

// TestDeleteSignal 覆盖删除信号：规范 0 0 0 00 可被接纳，混放/变体一律拒绝。
func TestDeleteSignal(t *testing.T) {
	f := newFixture(t)

	t.Run("canonical delete accepted after window", func(t *testing.T) {
		start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		clockPtr, clockFn := mutableClock(start)
		tr := newTestTracker(t, f, clockFn)
		del := []DSRecord{{KeyTag: 0, Algorithm: 0, DigestType: 0, Digest: []byte{0}}}
		for hour := 0; hour < 25; hour++ {
			at := start.Add(time.Duration(hour) * time.Hour)
			*clockPtr = at
			d, err := tr.Observe(consistentPolls(t, f, at, del))
			if hour == 24 {
				if err != nil || d.Action != ActionDelete {
					t.Fatalf("want delete, got action=%q err=%v", d.Action, err)
				}
				if d.NewDS != nil {
					t.Fatalf("delete decision must not carry DS records: %+v", d.NewDS)
				}
			} else if !errors.Is(err, ErrObservationShort) {
				t.Fatalf("hour %d: %v", hour, err)
			}
		}
	})

	t.Run("delete mixed with normal CDS rejected", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		mixed := []DSRecord{
			{KeyTag: 0, Algorithm: 0, DigestType: 0, Digest: []byte{0}},
			dsFor(t, f.zone, f.ksk2.rec, DigestSHA256),
		}
		if _, err := tr.Observe(consistentPolls(t, f, clock, mixed)); !errors.Is(err, ErrDeleteMixed) {
			t.Fatalf("want ErrDeleteMixed, got %v", err)
		}
	})

	t.Run("empty-digest delete variant rejected", func(t *testing.T) {
		clock := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
		tr := newTestTracker(t, f, func() time.Time { return clock })
		bad := []DSRecord{{KeyTag: 0, Algorithm: 0, DigestType: 0, Digest: nil}}
		_, err := tr.Observe(consistentPolls(t, f, clock, bad))
		if !errors.Is(err, ErrDeleteMalformed) && !errors.Is(err, ErrDeleteMixed) {
			t.Fatalf("want malformed/mixed delete error, got %v", err)
		}
	})
}
