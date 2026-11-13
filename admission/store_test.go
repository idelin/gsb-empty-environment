package admission

import (
	"errors"
	"testing"
	"time"
)

func storeOpts() Options {
	return unlimitedOpts((&fakeVerifier{}).verify)
}

// 场景 5a：合并时被丢弃的第三方签名不会因后续更新被加回。
func TestMergeTombstonePreventsResurrection(t *testing.T) {
	base := baseTime()
	store := NewStore()

	good := thirdSig(goodIssuer, 1, base)
	junk := thirdSig(badIssuer, 99, base) // 非白名单，首次即丢弃并进墓碑
	first := packetWith(uidWith("grace", base, selfSig(1, false, base), good, junk))

	res, err := store.Merge(first, storeOpts())
	if err != nil {
		t.Fatalf("first merge failed: %v", err)
	}
	kept := findUID(res.Packet, "grace")
	assertf(t, countThird(*kept) == 1, "only whitelisted sig should survive")
	assertf(t, hasDigest(res.Tombstones, junk), "junk should be tombstoned")

	// 第二次更新重新提交同一垃圾签名（即使它“看起来”有效）。
	second := packetWith(uidWith("grace", base, selfSig(1, false, base), junk))
	res2, err := store.Merge(second, storeOpts())
	if err != nil {
		t.Fatalf("second merge failed: %v", err)
	}
	kept2 := findUID(res2.Packet, "grace")
	if kept2 == nil {
		t.Fatal("uid grace missing after merge")
	}
	for _, sig := range kept2.Signatures {
		assertf(t, Digest(sig) != Digest(junk),
			"tombstoned signature was resurrected: %s", sig.Issuer)
	}
	assertf(t, hasDigest(res2.Tombstones, junk), "tombstone must persist across merges")

	// 之前保留的白名单签名仍在（按摘要去重，不会重复）。
	assertf(t, countThird(*kept2) == 1, "want 1 third-party after re-merge, got %d", countThird(*kept2))
}

// 场景 5b：伪造自签名的 UID 在后续更新中无法借第三方签名复活。
func TestMergeForgedUIDStaysRejected(t *testing.T) {
	base := baseTime()
	store := NewStore()

	attacker := thirdSig(goodIssuer, 7, base)
	forged := packetWith(uidWith("heidi", base, selfSig(1, true, base), attacker))
	res, err := store.Merge(forged, storeOpts())
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	assertf(t, res.Stats.UIDsKept == 0, "forged uid should not be admitted")
	assertf(t, hasDigest(res.Tombstones, attacker), "attacker sig should be tombstoned")

	// 后续更新附带一个合法自签名，同时重放历史第三方签名。
	legit := packetWith(uidWith("heidi", base,
		selfSig(2, false, base.Add(time.Hour)), attacker))
	res2, err := store.Merge(legit, storeOpts())
	if err != nil {
		t.Fatalf("second merge failed: %v", err)
	}
	kept := findUID(res2.Packet, "heidi")
	if kept == nil {
		t.Fatal("uid should now be accepted via valid self-sig")
	}
	for _, sig := range kept.Signatures {
		assertf(t, Digest(sig) != Digest(attacker),
			"previously tombstoned third-party sig must not return")
	}
}

// 场景 5c：重复合并是幂等的，签名按摘要去重。
func TestMergeIdempotent(t *testing.T) {
	base := baseTime()
	store := NewStore()
	pk := packetWith(uidWith("ivan", base,
		selfSig(1, false, base), thirdSig(goodIssuer, 1, base)))

	r1, err := store.Merge(pk, storeOpts())
	if err != nil {
		t.Fatalf("merge 1 failed: %v", err)
	}
	r2, err := store.Merge(pk, storeOpts())
	if err != nil {
		t.Fatalf("merge 2 failed: %v", err)
	}
	kept1 := findUID(r1.Packet, "ivan")
	kept2 := findUID(r2.Packet, "ivan")
	assertf(t, countThird(*kept2) == countThird(*kept1), "repeat merge must not duplicate sigs")
	assertf(t, store.Len() == 1, "one key expected, got %d", store.Len())
}

func TestMergeFingerprintMismatch(t *testing.T) {
	base := baseTime()
	pk := packetWith(uidWith("judy", base, selfSig(1, false, base)))
	existing := &StoredKey{
		Packet:     pk,
		Tombstones: map[string]struct{}{},
	}

	other := packetWith(uidWith("judy", base, selfSig(1, false, base)))
	other.Fingerprint = "DIFFERENT-FINGERPRINT"
	_, err := MergeInto(existing, other, storeOpts())
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want ErrFingerprintMismatch, got %v", err)
	}
}

// 超过 1 MiB 的裁剪结果必须被拒绝。
func TestOversizedPacketRejected(t *testing.T) {
	base := baseTime()
	v := &fakeVerifier{}
	pk := packetWith(uidWith("kyle", base, selfSig(1, false, base)))

	huge := func(p *PublicKeyPacket) ([]byte, error) {
		return make([]byte, MaxPacketSize+1), nil
	}
	opts := Options{Whitelist: NewWhitelist(goodIssuer), Budget: -1, Verify: v.verify, Serialize: huge}
	_, err := Admit(pk, opts)
	if !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("want ErrPacketTooLarge, got %v", err)
	}

	// 默认序列化器下，恰好 1 MiB 边界应被接受。
	ok := func(p *PublicKeyPacket) ([]byte, error) {
		return make([]byte, MaxPacketSize), nil
	}
	optsOK := Options{Whitelist: NewWhitelist(goodIssuer), Budget: -1, Verify: v.verify, Serialize: ok}
	if _, err := Admit(pk, optsOK); err != nil {
		t.Fatalf("boundary size should pass: %v", err)
	}
}

// 默认序列化器对体积的估算与 Raw 内容总量一致且稳定。
func TestDefaultSerializerDeterministic(t *testing.T) {
	base := baseTime()
	pk := packetWith(uidWith("leo", base,
		selfSig(1, false, base), thirdSig(goodIssuer, 1, base)))
	pk.Subkeys = append(pk.Subkeys, Subkey{Raw: []byte("sub-1"), Created: base})

	b1, err := DefaultSerializer(pk)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	b2, _ := DefaultSerializer(pk)
	assertf(t, len(b1) == len(b2), "serializer must be deterministic")

	chunks := 1 + len(pk.UserIDs) + len(pk.UserIDs[0].Signatures) + len(pk.Subkeys)
	rawTotal := len(pk.PrimaryRaw) + len(pk.UserIDs[0].Raw) +
		len(pk.UserIDs[0].Signatures[0].Raw) +
		len(pk.UserIDs[0].Signatures[1].Raw) + len(pk.Subkeys[0].Raw)
	expected := chunks*4 + rawTotal
	assertf(t, len(b1) == expected, "size estimate mismatch: %d vs %d", len(b1), expected)
}

// 上万第三方签名的端到端合并压力验证：两次合并后状态有界。
func TestFloodEndToEndMerge(t *testing.T) {
	base := baseTime()
	store := NewStore()

	build := func() *PublicKeyPacket {
		sigs := []Signature{selfSig(1, false, base)}
		for i := 0; i < 10000; i++ {
			issuer := badIssuer
			if i%50 == 0 {
				issuer = goodIssuer
			}
			sigs = append(sigs, thirdSig(issuer, i, base.Add(time.Duration(i)*time.Millisecond)))
		}
		return packetWith(uidWith("mona", base, sigs...))
	}

	r1, err := store.Merge(build(), storeOpts())
	if err != nil {
		t.Fatalf("merge 1 failed: %v", err)
	}
	kept := findUID(r1.Packet, "mona")
	assertf(t, countThird(*kept) <= MaxThirdPartyPerUID, "cap violated: %d", countThird(*kept))

	r2, err := store.Merge(build(), storeOpts())
	if err != nil {
		t.Fatalf("merge 2 failed: %v", err)
	}
	kept2 := findUID(r2.Packet, "mona")
	assertf(t, countThird(*kept2) <= MaxThirdPartyPerUID, "cap violated after re-merge")
	assertf(t, countThird(*kept2) == countThird(*kept), "re-merge changed bounded state")
}
