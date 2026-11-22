package keyusage

import (
	"errors"
	"testing"
)

func denialReason(t *testing.T, err error) DenialReason {
	t.Helper()
	var de *DenialError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DenialError, got %T: %v", err, err)
	}
	return de.Reason
}

func assertDenied(t *testing.T, err error, want DenialReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected denial with reason %s, got nil", want)
	}
	if got := denialReason(t, err); got != want {
		t.Fatalf("expected denial reason %s, got %s", want, got)
	}
}

func assertAllowed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected call to be allowed, got: %v", err)
	}
}

func newEnforcerWithKey(t *testing.T, k Key) (*Enforcer, *MemorySink) {
	t.Helper()
	sink := NewMemorySink()
	enf := NewEnforcer(sink)
	if err := enf.Register(k); err != nil {
		t.Fatalf("register key %q: %v", k.ID, err)
	}
	return enf, sink
}

// 错用：签名密钥用于加密、加密密钥用于签名、未声明用途均被拒绝。
func TestMisuseCrossFamilyAndUndeclared(t *testing.T) {
	signKey := Key{ID: "sig-1", Algorithm: AlgECDSAP256, Usages: []Usage{UsageSign, UsageVerify}}
	encKey := Key{ID: "enc-1", Algorithm: AlgAESGCM256, Usages: []Usage{UsageEncrypt, UsageDecrypt}}

	enf, sink := newEnforcerWithKey(t, signKey)
	if err := enf.Register(encKey); err != nil {
		t.Fatalf("register enc key: %v", err)
	}

	// 合法调用放行。
	assertAllowed(t, enf.Check(signKey, UsageSign))
	assertAllowed(t, enf.Check(signKey, UsageVerify))
	assertAllowed(t, enf.Check(encKey, UsageEncrypt))
	assertAllowed(t, enf.Check(encKey, UsageDecrypt))

	// 签名密钥用于加解密：跨用途族。
	assertDenied(t, enf.Check(signKey, UsageEncrypt), ReasonCrossFamilyUsage)
	assertDenied(t, enf.Check(signKey, UsageDecrypt), ReasonCrossFamilyUsage)

	// 加密密钥用于签名/验签：跨用途族。
	assertDenied(t, enf.Check(encKey, UsageSign), ReasonCrossFamilyUsage)
	assertDenied(t, enf.Check(encKey, UsageVerify), ReasonCrossFamilyUsage)

	// 声明了加密但未声明包装：同用途族内未声明。
	assertDenied(t, enf.Check(encKey, UsageWrapKey), ReasonUsageNotDeclared)

	// 合法调用不产生审计，拒绝调用全部留痕。
	if got := len(sink.Events()); got != 5 {
		t.Fatalf("expected 5 denial events, got %d: %+v", got, sink.Events())
	}
	for _, ev := range sink.Events() {
		if ev.Time.IsZero() || ev.Reason == "" {
			t.Fatalf("audit event missing fields: %+v", ev)
		}
	}
}

// 同一密钥不得同时声明签名与保密用途。
func TestRejectMixedFamilyRegistration(t *testing.T) {
	sink := NewMemorySink()
	enf := NewEnforcer(sink)

	// 登记阶段声明非法统一返回 invalid_key，具体细节见审计 Detail。
	err := enf.Register(Key{ID: "mixed", Algorithm: AlgAESGCM256,
		Usages: []Usage{UsageEncrypt, UsageSign}})
	assertDenied(t, err, ReasonInvalidKey)

	// RSA-OAEP 白名单只给保密用途，声明 verify 同样属于非法声明。
	k := Key{ID: "mixed-2", Algorithm: AlgRSAOAEP256,
		Usages: []Usage{UsageEncrypt, UsageVerify}}
	assertDenied(t, enf.Register(k), ReasonInvalidKey)

	// 直接验证族冲突判定函数。
	mixed := Key{Usages: []Usage{UsageSign, UsageEncrypt}}
	if !crossFamilyViolation(mixed, UsageDecrypt) {
		t.Fatal("expected cross-family violation for sign key decrypt")
	}
	if !mixed.hasUsageFamilyConflict() {
		t.Fatal("expected hasUsageFamilyConflict")
	}
}

// 弱算法与非白名单算法一律拒绝并记录。
func TestWeakAndNonWhitelistedAlgorithms(t *testing.T) {
	sink := NewMemorySink()
	enf := NewEnforcer(sink)

	weakKey := Key{ID: "weak", Algorithm: Alg3DESCBC, Usages: []Usage{UsageEncrypt}}

	// 登记阶段即拒绝弱算法。
	assertDenied(t, enf.Register(weakKey), ReasonInvalidKey)

	// 即使绕过登记直接调用 Check，弱算法也被拒绝并按弱算法原因记录。
	assertDenied(t, enf.Check(weakKey, UsageEncrypt), ReasonWeakAlgorithm)

	unknownKey := Key{ID: "unknown", Algorithm: Algorithm("ROT13"), Usages: []Usage{UsageEncrypt}}
	assertDenied(t, enf.Check(unknownKey, UsageEncrypt), ReasonAlgorithmNotWhitelisted)

	// MD5/RSA1_5/SHA1 等均识别为弱算法。
	for _, alg := range []Algorithm{AlgMD5, AlgRSA15, AlgSHA1, AlgRC4, AlgDESCBC, AlgECDSAP192} {
		if !IsWeak(alg) {
			t.Fatalf("algorithm %s should be classified weak", alg)
		}
	}

	events := sink.Events()
	var sawWeak, sawNotWhitelisted bool
	for _, ev := range events {
		if ev.Reason == ReasonWeakAlgorithm && ev.Algorithm == Alg3DESCBC {
			sawWeak = true
		}
		if ev.Reason == ReasonAlgorithmNotWhitelisted {
			sawNotWhitelisted = true
		}
	}
	if !sawWeak || !sawNotWhitelisted {
		t.Fatalf("expected weak and non-whitelisted audit events, got %+v", events)
	}
}

// 算法白名单能力约束：签名算法不能用于加密操作。
func TestAlgorithmUsageMismatch(t *testing.T) {
	enf, sink := newEnforcerWithKey(t, Key{ID: "hmac", Algorithm: AlgHMACSHA256,
		Usages: []Usage{UsageSign, UsageVerify}})

	// HMAC 不在加密能力集合内，且密钥属签名族：族冲突优先。
	assertDenied(t, enf.Check(Key{ID: "hmac", Algorithm: AlgHMACSHA256,
		Usages: []Usage{UsageSign}}, UsageEncrypt), ReasonCrossFamilyUsage)

	// 同用途族内、白名单算法不支持请求用途（ChaCha20 无密钥包装能力）。
	chacha := Key{ID: "chacha", Algorithm: AlgChaCha20Poly,
		Usages: []Usage{UsageEncrypt, UsageDecrypt}}
	assertDenied(t, enf.Check(chacha, UsageWrapKey), ReasonAlgorithmUsageMismatch)

	// 未知用途。
	kdf := Key{ID: "kdf", Algorithm: AlgHKDFSHA256, Usages: []Usage{UsageDerive}}
	assertDenied(t, enf.Check(kdf, Usage("teleport")), ReasonUnknownUsage)

	if len(sink.Events()) != 3 {
		t.Fatalf("expected 3 events, got %d", len(sink.Events()))
	}
}

// 包装密钥只能包装/解包密钥，不得加解密业务数据。
func TestWrapKeyCannotTouchBusinessData(t *testing.T) {
	wrapKey := Key{ID: "wrap-1", Algorithm: AlgAESKeyWrap256,
		Usages: []Usage{UsageWrapKey, UsageUnwrapKey}}
	enf, sink := newEnforcerWithKey(t, wrapKey)

	// 包装/解包其他密钥放行。
	assertAllowed(t, enf.Check(wrapKey, UsageWrapKey))
	assertAllowed(t, enf.Check(wrapKey, UsageUnwrapKey))

	// 用于业务数据加解密：专用拒绝原因。
	assertDenied(t, enf.Check(wrapKey, UsageEncrypt), ReasonWrapKeyForData)
	assertDenied(t, enf.Check(wrapKey, UsageDecrypt), ReasonWrapKeyForData)

	// AES-GCM 同时声明业务加解密与包装时，业务数据操作应放行（非纯包装密钥）。
	dual := Key{ID: "dual-1", Algorithm: AlgAESGCM256,
		Usages: []Usage{UsageEncrypt, UsageDecrypt, UsageWrapKey, UsageUnwrapKey}}
	if err := enf.Register(dual); err != nil {
		t.Fatalf("register dual key: %v", err)
	}
	assertAllowed(t, enf.Check(dual, UsageEncrypt))
	assertAllowed(t, enf.Check(dual, UsageWrapKey))

	for _, ev := range sink.Events() {
		if ev.Reason != ReasonWrapKeyForData {
			t.Fatalf("unexpected audit reason %s", ev.Reason)
		}
	}
}

// 派生密钥只能继承父用途的子集；父密钥未声明 derive 不得派生。
func TestDerivationInheritsParentUsageSubset(t *testing.T) {
	parent := Key{ID: "parent", Algorithm: AlgHKDFSHA256,
		Usages: []Usage{UsageDerive, UsageEncrypt, UsageDecrypt}}
	enf, sink := newEnforcerWithKey(t, parent)

	// 合法派生：子用途为空时拒绝；非空且为父集合子集时放行。
	_, err := enf.Derive("parent", "child-empty", AlgAESGCM256, nil)
	assertDenied(t, err, ReasonEmptyDerivedUsages)

	child, err := enf.Derive("parent", "child-ok", AlgAESGCM256,
		[]Usage{UsageEncrypt})
	if err != nil {
		t.Fatalf("expected derivation allowed: %v", err)
	}
	if !child.IsDerived() || child.ParentID != "parent" {
		t.Fatalf("derived key metadata wrong: %+v", child)
	}

	// 父密钥只有 derive，子密钥想要 encrypt/decrypt 都超出父用途集合。
	_, err = enf.Derive("parent", "child-escalate", AlgAESGCM256,
		[]Usage{UsageEncrypt, UsageDecrypt, UsageSign})
	assertDenied(t, err, ReasonDerivedUsageExceedsParent)

	// 子密钥请求父集合之外的用途同样被拒（sign 父根本没有）。
	_, err = enf.Derive("parent", "child-sign", AlgECDSAP256, []Usage{UsageSign})
	assertDenied(t, err, ReasonDerivedUsageExceedsParent)

	// 子算法支持该用途、但父密钥未授权该用途时，子集约束拒绝（wrap 不在父集合中）。
	_, err = enf.Derive("parent", "child-badalg", AlgAESGCM256, []Usage{UsageWrapKey})
	assertDenied(t, err, ReasonDerivedUsageExceedsParent)

	// 成功登记的派生密钥可通过 Get 取回并正常执行其继承到的用途。
	got, ok := enf.Key("child-ok")
	if !ok {
		t.Fatal("derived child not registered")
	}
	assertAllowed(t, enf.Check(got, UsageEncrypt))
	assertDenied(t, enf.Check(got, UsageDecrypt), ReasonUsageNotDeclared)

	// 每个失败派生都留痕。
	reasons := map[DenialReason]int{}
	for _, ev := range sink.Events() {
		reasons[ev.Reason]++
	}
	if reasons[ReasonDerivedUsageExceedsParent] != 3 {
		t.Fatalf("expected 3 subset-exceed events, got %d", reasons[ReasonDerivedUsageExceedsParent])
	}
	if reasons[ReasonEmptyDerivedUsages] != 1 {
		t.Fatalf("expected 1 empty-usages event, got %d", reasons[ReasonEmptyDerivedUsages])
	}
}

// 非派生父密钥（未声明 derive）拒绝派生，防止派生滥用。
func TestDeriveRequiresDeriveUsage(t *testing.T) {
	encParent := Key{ID: "enc-parent", Algorithm: AlgAESGCM256,
		Usages: []Usage{UsageEncrypt, UsageDecrypt}}
	enf, sink := newEnforcerWithKey(t, encParent)

	_, err := enf.Derive("enc-parent", "kid", AlgAESGCM256, []Usage{UsageEncrypt})
	assertDenied(t, err, ReasonDeriveNotPermitted)

	// 不存在的父密钥。
	_, err = enf.Derive("ghost", "kid-2", AlgAESGCM256, []Usage{UsageEncrypt})
	assertDenied(t, err, ReasonInvalidKey)

	if len(sink.Events()) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(sink.Events()))
	}
}

// 多级派生：子集约束沿派生链逐级生效。
func TestChainedDerivationSubset(t *testing.T) {
	root := Key{ID: "root", Algorithm: AlgHKDFSHA256,
		Usages: []Usage{UsageDerive, UsageEncrypt, UsageDecrypt}}
	enf, _ := newEnforcerWithKey(t, root)

	mid, err := enf.Derive("root", "mid", AlgHKDFSHA256,
		[]Usage{UsageDerive, UsageEncrypt})
	if err != nil {
		t.Fatalf("derive mid: %v", err)
	}

	// 孙密钥只能取 mid 的子集；decrypt 在 mid 中已不存在，故被拒。
	_, err = enf.Derive(mid.ID, "grand", AlgAESGCM256,
		[]Usage{UsageEncrypt, UsageDecrypt})
	assertDenied(t, err, ReasonDerivedUsageExceedsParent)

	grand, err := enf.Derive(mid.ID, "grand-ok", AlgAESGCM256,
		[]Usage{UsageEncrypt})
	if err != nil {
		t.Fatalf("derive grand: %v", err)
	}
	if grand.ParentID != "mid" {
		t.Fatalf("unexpected parent: %s", grand.ParentID)
	}
}

// 成功路径不会污染审计日志。
func TestAllowedCallsProduceNoAudit(t *testing.T) {
	enf, sink := newEnforcerWithKey(t, Key{ID: "ok", Algorithm: AlgEd25519,
		Usages: []Usage{UsageSign, UsageVerify}})

	for range 5 {
		assertAllowed(t, enf.Check(Key{ID: "ok", Algorithm: AlgEd25519,
			Usages: []Usage{UsageSign}}, UsageSign))
	}
	if len(sink.Events()) != 0 {
		t.Fatalf("expected no audit events, got %d", len(sink.Events()))
	}
}
