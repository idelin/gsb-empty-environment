package admission

import (
	"testing"
	"time"
)

func unlimitedOpts(verifier VerifyFunc) Options {
	return Options{
		Whitelist: NewWhitelist(goodIssuer, goodIssuer2),
		Budget:    -1,
		Verify:    verifier,
	}
}

// 场景 1：上万条第三方签名泛滥，只保留白名单且每 UID 最多 50 条。
func TestThirdPartyFloodKeepsOnlyWhitelistAndCap(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()

	sigs := []Signature{selfSig(1, false, base)}
	const flood = 12000
	for i := 0; i < flood; i++ {
		issuer := badIssuer
		if i%3 == 0 {
			issuer = goodIssuer
		}
		if i%7 == 0 {
			issuer = goodIssuer2
		}
		sigs = append(sigs, thirdSig(issuer, i, base.Add(time.Duration(i)*time.Second)))
	}
	pk := packetWith(uidWith("alice", base, sigs...))

	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}

	assertf(t, res.Stats.UIDsKept == 1, "want 1 kept uid, got %d", res.Stats.UIDsKept)
	kept := findUID(res.Packet, "alice")
	if kept == nil {
		t.Fatal("uid alice missing")
	}
	assertf(t, countThird(*kept) == MaxThirdPartyPerUID,
		"want %d third-party, got %d", MaxThirdPartyPerUID, countThird(*kept))

	for _, sig := range kept.Signatures {
		if sig.Self {
			continue
		}
		assertf(t, sig.Issuer == goodIssuer || sig.Issuer == goodIssuer2,
			"non-whitelisted issuer kept: %s", sig.Issuer)
	}

	// 保留的应是创建时间最新的 50 条有效白名单签名（输入按时间递增），
	// 且整体按时间降序排列。
	var newestThird time.Time
	var whitelistCount int
	for _, sig := range kept.Signatures {
		if sig.Self {
			continue
		}
		whitelistCount++
		if sig.Created.After(newestThird) {
			newestThird = sig.Created
		}
	}
	assertf(t, whitelistCount == MaxThirdPartyPerUID, "want 50 retained, got %d", whitelistCount)
	// 输入按时间递增，最后一个白名单签名（i%3==0 或 i%7==0）应被保留。
	lastWhitelistIndex := -1
	for i := flood - 1; i >= 0; i-- {
		if i%3 == 0 || i%7 == 0 {
			lastWhitelistIndex = i
			break
		}
	}
	assertf(t, newestThird.Equal(base.Add(time.Duration(lastWhitelistIndex)*time.Second)),
		"newest whitelisted signature must be retained, got %v", newestThird)
	var prev time.Time
	for _, sig := range kept.Signatures {
		if sig.Self {
			continue
		}
		if !prev.IsZero() && sig.Created.After(prev) {
			t.Fatal("kept third-party signatures must be newest-first")
		}
		prev = sig.Created
	}

	// 白名单之外的洪峰签名被墓碑化，且验证预算未被垃圾签名耗尽
	// （非白名单直接拒绝，不触发回调）。
	assertf(t, res.Stats.ThirdPartyIn == flood, "want %d in, got %d", flood, res.Stats.ThirdPartyIn)
	assertf(t, res.Stats.ThirdPartyRejected > 0, "expected non-whitelist rejections")
	assertf(t, res.Stats.ThirdPartyCapped > 0, "expected cap rejections")
	assertf(t, len(res.Tombstones) > 0, "expected tombstones for flood signatures")

	// 被丢弃的垃圾签名确实在墓碑中。
	var garbage Signature
	for _, sig := range sigs {
		if !sig.Self && sig.Issuer == badIssuer {
			garbage = sig
			break
		}
	}
	assertf(t, hasDigest(res.Tombstones, garbage), "garbage signature not tombstoned")

	// 输入未被修改。
	assertf(t, len(pk.UserIDs[0].Signatures) == flood+1, "input packet mutated")
}

// 场景 2a：伪造自签名导致整个用户标识丢弃，且其上签名进入墓碑。
func TestForgedSelfSignatureDropsWholeUID(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	good := thirdSig(goodIssuer, 1, base)
	forged := uidWith("mallory", base,
		selfSig(1, true, base), // forged: 验证失败
		good,
	)
	pk := packetWith(forged)

	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	assertf(t, res.Stats.UIDsKept == 0, "want 0 kept, got %d", res.Stats.UIDsKept)
	assertf(t, res.Stats.UIDsDropped == 1, "want 1 dropped, got %d", res.Stats.UIDsDropped)
	assertf(t, res.Stats.SelfSigsRejected == 1, "want 1 rejected self-sig")
	assertf(t, hasDigest(res.Tombstones, good),
		"third-party sig on dropped uid should be tombstoned")
}

// 场景 2b：存在多条自签名时，保留最新且可验证的一条。
func TestNewestValidSelfSignatureChosen(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	old := selfSig(1, false, base)
	newer := selfSig(2, false, base.Add(2*time.Hour))
	forgedNewest := selfSig(3, true, base.Add(3*time.Hour))

	pk := packetWith(uidWith("bob", base, old, newer, forgedNewest))
	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	kept := findUID(res.Packet, "bob")
	if kept == nil {
		t.Fatal("uid bob should be kept")
	}
	var selfCount int
	var chosen Signature
	for _, sig := range kept.Signatures {
		if sig.Self {
			selfCount++
			chosen = sig
		}
	}
	assertf(t, selfCount == 1, "want exactly 1 self-signature, got %d", selfCount)
	assertf(t, Digest(chosen) == Digest(newer), "want newest valid self-sig chosen")
	assertf(t, hasDigest(res.Tombstones, old), "older self-sig should be tombstoned")
	assertf(t, hasDigest(res.Tombstones, forgedNewest), "forged self-sig should be tombstoned")
}

// 场景 3：超过 20 个子密钥时按创建时间保留最新的 20 个，但不拒绝公钥。
func TestSubkeyCapKeepsNewest(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	pk := packetWith(uidWith("carol", base, selfSig(1, false, base)))
	for i := 0; i < 35; i++ {
		pk.Subkeys = append(pk.Subkeys, Subkey{
			Raw:     []byte("sub-" + time.Duration(i).String()),
			Created: base.Add(time.Duration(i) * time.Hour),
		})
	}

	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	assertf(t, res.Stats.SubkeysIn == 35, "want 35 subkeys in")
	assertf(t, res.Stats.SubkeysKept == MaxSubkeys,
		"want %d kept, got %d", MaxSubkeys, res.Stats.SubkeysKept)

	oldestKept := res.Packet.Subkeys[MaxSubkeys-1]
	assertf(t, oldestKept.Created.Equal(base.Add(15*time.Hour)),
		"want oldest kept subkey index 15, got %v", oldestKept.Created)
}

// 场景 4a：重复提交（同内容签名）按摘要去重。
func TestDuplicateSubmissionDedup(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	sig := thirdSig(goodIssuer, 1, base)
	sigs := []Signature{selfSig(1, false, base), sig, sig, sig}
	pk := packetWith(uidWith("dave", base, sigs...))

	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	kept := findUID(res.Packet, "dave")
	assertf(t, countThird(*kept) == 1, "want 1 deduped third-party, got %d", countThird(*kept))
	assertf(t, res.Stats.ThirdPartyDuplicate == 2,
		"want 2 duplicate drops, got %d", res.Stats.ThirdPartyDuplicate)
}

// 场景 4b：验证预算约束实际回调次数。
func TestBudgetLimitsVerifications(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	sigs := []Signature{selfSig(1, false, base)}
	for i := 0; i < 10; i++ {
		sigs = append(sigs, thirdSig(goodIssuer, i, base.Add(time.Duration(i)*time.Second)))
	}
	pk := packetWith(uidWith("erin", base, sigs...))

	opts := Options{Whitelist: NewWhitelist(goodIssuer), Budget: 3, Verify: v.verify}
	res, err := Admit(pk, opts)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	assertf(t, v.calls == 3, "want 3 verifications, got %d", v.calls)
	assertf(t, res.Stats.VerificationsUsed == 3, "stats mismatch")
	assertf(t, res.Stats.ThirdPartyDeferred > 0, "want deferred third-party sigs")
}

// 预算为 0 时不验证任何签名：用户标识被静默丢弃但不写墓碑（可重试）。
func TestZeroBudgetDropsUIDWithoutTombstone(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	sig := thirdSig(goodIssuer, 1, base)
	pk := packetWith(uidWith("frank", base, selfSig(1, false, base), sig))

	opts := Options{Whitelist: NewWhitelist(goodIssuer), Budget: 0, Verify: v.verify}
	res, err := Admit(pk, opts)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	assertf(t, v.calls == 0, "no verification expected")
	assertf(t, res.Stats.UIDsKept == 0, "uid should be dropped without verification")
	assertf(t, len(res.Tombstones) == 0, "deferred sigs must not be tombstoned")
}

// 超过 10 个用户标识时保留最新 10 个，不拒绝整个公钥。
func TestUIDCapKeepsNewest(t *testing.T) {
	v := &fakeVerifier{}
	base := baseTime()
	pk := &PublicKeyPacket{Fingerprint: testFP, PrimaryRaw: []byte("primary")}
	letters := "abcdefghijklmn"
	for i := 0; i < 14; i++ {
		pk.UserIDs = append(pk.UserIDs, uidWith(
			string(letters[i]),
			base.Add(time.Duration(i)*time.Hour),
			selfSig(i, false, base),
		))
	}
	res, err := Admit(pk, unlimitedOpts(v.verify))
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	assertf(t, res.Stats.UIDsKept == MaxUIDs, "want %d uids, got %d", MaxUIDs, res.Stats.UIDsKept)
	newest := findUID(res.Packet, "n") // index 13
	oldest := findUID(res.Packet, "e") // index 4 (14-10)
	assertf(t, newest != nil, "newest uid should be kept")
	assertf(t, oldest != nil, "uid index 4 should be kept")
	assertf(t, findUID(res.Packet, "a") == nil, "oldest uid should be dropped")
	assertf(t, findUID(res.Packet, "d") == nil, "uid index 3 should be dropped")
}

func TestNilInputsRejected(t *testing.T) {
	_, err := Admit(nil, Options{Verify: (&fakeVerifier{}).verify})
	if err == nil {
		t.Fatal("nil packet should be rejected")
	}
	if _, ok := err.(invalidError); !ok {
		t.Fatalf("want invalidError, got %T", err)
	}
	if _, err := Admit(packetWith(), Options{}); err == nil {
		t.Fatal("nil verify callback should be rejected")
	}
}
