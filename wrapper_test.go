package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// observableUnwrapper 是可观察的解包器: 模拟密钥服务把认证上下文
// 绑定进密文数据密钥, 并记录每一次回调收到的完整请求。
type observableUnwrapper struct {
	mu        sync.Mutex
	requests  []UnwrapRequest
	unwrapErr error // 若非 nil, 强制所有解包失败
}

// wrap 模拟密钥服务加密: edk = "EDK1" || u32len(authCtx) || authCtx || dek。
func (o *observableUnwrapper) wrap(authContext, dek []byte) []byte {
	edk := make([]byte, 0, 4+4+len(authContext)+len(dek))
	edk = append(edk, "EDK1"...)
	edk = append(edk, byte(len(authContext)>>24), byte(len(authContext)>>16),
		byte(len(authContext)>>8), byte(len(authContext)))
	edk = append(edk, authContext...)
	edk = append(edk, dek...)
	return edk
}

func (o *observableUnwrapper) Unwrap(_ context.Context, req UnwrapRequest) ([]byte, error) {
	o.mu.Lock()
	// 深拷贝请求, 防止调用方事后修改影响观测结果。
	recorded := UnwrapRequest{
		MasterKeyVersion: req.MasterKeyVersion,
		EncryptedDEK:     append([]byte(nil), req.EncryptedDEK...),
		AuthContext:      append([]byte(nil), req.AuthContext...),
	}
	o.requests = append(o.requests, recorded)
	forcedErr := o.unwrapErr
	o.mu.Unlock()

	if forcedErr != nil {
		return nil, forcedErr
	}
	edk := req.EncryptedDEK
	if len(edk) < 8 || string(edk[:4]) != "EDK1" {
		return nil, errors.New("kms: malformed ciphertext data key")
	}
	n := int(edk[4])<<24 | int(edk[5])<<16 | int(edk[6])<<8 | int(edk[7])
	if len(edk) < 8+n {
		return nil, errors.New("kms: malformed ciphertext data key")
	}
	boundCtx := edk[8 : 8+n]
	if !bytes.Equal(boundCtx, req.AuthContext) {
		return nil, fmt.Errorf("kms: authenticated context mismatch")
	}
	return append([]byte(nil), edk[8+n:]...), nil
}

func (o *observableUnwrapper) calls() []UnwrapRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]UnwrapRequest(nil), o.requests...)
}

// staticMasterKeys 是可编程的主密钥版本源。
type staticMasterKeys struct {
	mu       sync.Mutex
	current  string
	disabled map[string]bool
}

func newStaticMasterKeys(current string) *staticMasterKeys {
	return &staticMasterKeys{current: current, disabled: map[string]bool{}}
}

func (s *staticMasterKeys) Current() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, nil
}

func (s *staticMasterKeys) Enabled(version string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disabled[version]
}

func (s *staticMasterKeys) disable(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled[version] = true
}

func (s *staticMasterKeys) setCurrent(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = version
}

func randDEK(t *testing.T) []byte {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	return dek
}

func newTestRig(t *testing.T, version string) (*Wrapper, *observableUnwrapper, *staticMasterKeys) {
	t.Helper()
	obs := &observableUnwrapper{}
	keys := newStaticMasterKeys(version)
	w, err := NewWrapper(obs, keys)
	if err != nil {
		t.Fatal(err)
	}
	return w, obs, keys
}

// TestUnwrapCachesByContextAndMasterKey 验证相同 (上下文, EDK, 主钥版本)
// 只触发一次解包回调, 且回调收到的上下文与规范编码逐字节一致。
func TestUnwrapCachesByContextAndMasterKey(t *testing.T) {
	w, obs, _ := newTestRig(t, "mk-v1")
	actx := Context{Tenant: "tenant-a", Purpose: "blob-encryption", DataVersion: 7}
	dek := randDEK(t)
	edk := obs.wrap(actx.MustCanonical(), dek)

	for i := 0; i < 3; i++ {
		got, err := w.Unwrap(context.Background(), actx, edk)
		if err != nil {
			t.Fatalf("unwrap %d: %v", i, err)
		}
		if !bytes.Equal(got, dek) {
			t.Fatalf("unwrap %d: plaintext mismatch", i)
		}
	}

	calls := obs.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 unwrap callback, got %d", len(calls))
	}
	if !bytes.Equal(calls[0].AuthContext, actx.MustCanonical()) {
		t.Fatalf("callback received wrong auth context:\n got %q\nwant %q",
			calls[0].AuthContext, actx.MustCanonical())
	}
	if calls[0].MasterKeyVersion != "mk-v1" {
		t.Fatalf("callback master key version = %q, want mk-v1", calls[0].MasterKeyVersion)
	}

	// 缓存返回的明文必须是独立副本, 调用方修改不得污染缓存。
	got, _ := w.Unwrap(context.Background(), actx, edk)
	got[0] ^= 0xff
	again, _ := w.Unwrap(context.Background(), actx, edk)
	if !bytes.Equal(again, dek) {
		t.Fatal("cached plaintext was mutated through returned slice")
	}
}

// TestCrossProjectSubstitutionRejected 验证把密文数据密钥搬到另一项目后,
// 缓存不会按密文字节错配, 解包回调收到的是新项目的完整上下文并被拒绝。
func TestCrossProjectSubstitutionRejected(t *testing.T) {
	w, obs, _ := newTestRig(t, "mk-v1")
	ctxA := Context{Tenant: "project-a", Purpose: "blob-encryption", DataVersion: 1}
	ctxB := Context{Tenant: "project-b", Purpose: "blob-encryption", DataVersion: 1}
	dek := randDEK(t)
	edk := obs.wrap(ctxA.MustCanonical(), dek)

	// 项目 A 正常解包并进入缓存。
	if _, err := w.Unwrap(context.Background(), ctxA, edk); err != nil {
		t.Fatalf("project-a unwrap: %v", err)
	}
	if n := len(obs.calls()); n != 1 {
		t.Fatalf("expected 1 callback, got %d", n)
	}

	// 同一份 EDK 字节被搬到项目 B: 缓存键含上下文摘要, 必然未命中,
	// 回调必须收到项目 B 的上下文, 密钥服务据此拒绝。
	if _, err := w.Unwrap(context.Background(), ctxB, edk); err == nil {
		t.Fatal("cross-project substitution unexpectedly succeeded")
	}
	calls := obs.calls()
	if len(calls) != 2 {
		t.Fatalf("expected substitution to reach callback, got %d calls", len(calls))
	}
	if !bytes.Equal(calls[1].AuthContext, ctxB.MustCanonical()) {
		t.Fatalf("callback did not receive project-b context:\n got %q\nwant %q",
			calls[1].AuthContext, ctxB.MustCanonical())
	}
	if w.CacheSize() != 1 {
		t.Fatalf("failed unwrap must not populate cache, size = %d", w.CacheSize())
	}
}

// TestContextEncodingUnambiguous 验证规范编码无字段拼接歧义:
// ("ab","c") 与 ("a","bc") 编码不同, 缓存键也不同。
func TestContextEncodingUnambiguous(t *testing.T) {
	c1 := Context{Tenant: "ab", Purpose: "c", DataVersion: 1}
	c2 := Context{Tenant: "a", Purpose: "bc", DataVersion: 1}
	if bytes.Equal(c1.MustCanonical(), c2.MustCanonical()) {
		t.Fatal("ambiguous contexts produced identical canonical encodings")
	}

	w, obs, _ := newTestRig(t, "mk-v1")
	dek := randDEK(t)
	edk := obs.wrap(c1.MustCanonical(), dek)

	if _, err := w.Unwrap(context.Background(), c1, edk); err != nil {
		t.Fatalf("unwrap c1: %v", err)
	}
	// 歧义上下文必须穿透缓存再次调用回调, 并被密钥服务拒绝。
	if _, err := w.Unwrap(context.Background(), c2, edk); err == nil {
		t.Fatal("ambiguous context unexpectedly unwraped via cache or KMS")
	}
	if n := len(obs.calls()); n != 2 {
		t.Fatalf("ambiguous context must bypass cache, got %d callbacks", n)
	}

	// 数据版本同样参与编码: 仅版本不同也必须绕过缓存。
	c3 := Context{Tenant: "ab", Purpose: "c", DataVersion: 2}
	if _, err := w.Unwrap(context.Background(), c3, edk); err == nil {
		t.Fatal("different data version unexpectedly unwraped")
	}
	if n := len(obs.calls()); n != 3 {
		t.Fatalf("data version must be part of cache key, got %d callbacks", n)
	}
}

// TestDisabledMasterKeyInvalidatesCache 验证旧主钥禁用后:
// 缓存立即失效、条目被清除、解包返回 ErrMasterKeyDisabled;
// 轮换到新版本后缓存按新版本独立计数。
func TestDisabledMasterKeyInvalidatesCache(t *testing.T) {
	w, obs, keys := newTestRig(t, "mk-v1")
	actx := Context{Tenant: "tenant-a", Purpose: "ledger", DataVersion: 3}
	dek := randDEK(t)
	edk := obs.wrap(actx.MustCanonical(), dek)

	if _, err := w.Unwrap(context.Background(), actx, edk); err != nil {
		t.Fatalf("initial unwrap: %v", err)
	}
	if _, err := w.Unwrap(context.Background(), actx, edk); err != nil {
		t.Fatalf("cached unwrap: %v", err)
	}
	if n := len(obs.calls()); n != 1 {
		t.Fatalf("expected cache hit, got %d callbacks", n)
	}
	if w.CacheSize() != 1 {
		t.Fatalf("cache size = %d, want 1", w.CacheSize())
	}

	// 禁用旧主钥: 既有缓存不得再被使用。
	keys.disable("mk-v1")
	if _, err := w.Unwrap(context.Background(), actx, edk); !errors.Is(err, ErrMasterKeyDisabled) {
		t.Fatalf("expected ErrMasterKeyDisabled, got %v", err)
	}
	if n := len(obs.calls()); n != 1 {
		t.Fatalf("disabled key must not reach callback, got %d callbacks", n)
	}
	if w.CacheSize() != 0 {
		t.Fatalf("disabled version entries must be evicted, size = %d", w.CacheSize())
	}

	// 轮换到新主钥版本: 旧版本缓存不泄漏, 重新走回调并缓存到新键下。
	keys.setCurrent("mk-v2")
	if _, err := w.Unwrap(context.Background(), actx, edk); err != nil {
		t.Fatalf("unwrap under mk-v2: %v", err)
	}
	calls := obs.calls()
	if len(calls) != 2 {
		t.Fatalf("rotation must trigger new callback, got %d", len(calls))
	}
	if calls[1].MasterKeyVersion != "mk-v2" {
		t.Fatalf("callback version = %q, want mk-v2", calls[1].MasterKeyVersion)
	}
	if _, err := w.Unwrap(context.Background(), actx, edk); err != nil {
		t.Fatalf("cached unwrap under mk-v2: %v", err)
	}
	if n := len(obs.calls()); n != 2 {
		t.Fatalf("mk-v2 cache miss, got %d callbacks", n)
	}
}
