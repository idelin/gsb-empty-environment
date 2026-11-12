package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// UnwrapRequest 是发给密钥服务解包回调的请求。
// AuthContext 必须是 Context.Canonical() 的原样字节, 逐字节一致。
type UnwrapRequest struct {
	MasterKeyVersion string // 当前主密钥版本
	EncryptedDEK     []byte // 密文数据密钥
	AuthContext      []byte // 认证上下文的规范编码
}

// Unwrapper 是密钥服务(KMS)解包回调。
// 实现方必须按 AuthContext 验证密文数据密钥的绑定关系。
type Unwrapper interface {
	Unwrap(ctx context.Context, req UnwrapRequest) (plaintextDEK []byte, err error)
}

// UnwrapperFunc 让普通函数适配 Unwrapper。
type UnwrapperFunc func(ctx context.Context, req UnwrapRequest) ([]byte, error)

// Unwrap 实现 Unwrapper。
func (f UnwrapperFunc) Unwrap(ctx context.Context, req UnwrapRequest) ([]byte, error) {
	return f(ctx, req)
}

// MasterKeySource 提供主密钥版本状态。
type MasterKeySource interface {
	// Current 返回当前生效的主密钥版本。
	Current() (string, error)
	// Enabled 报告指定版本是否仍被允许用于解包。
	Enabled(version string) bool
}

// ErrMasterKeyDisabled 表示当前主密钥版本已被禁用, 拒绝解包。
var ErrMasterKeyDisabled = errors.New("netsec: master key version disabled")

// cacheKey 是明文数据密钥缓存的键。
// 仅按密文字节查找会把别的项目的密钥错配进来, 因此键必须同时包含
// 主密钥版本与认证上下文摘要。
type cacheKey struct {
	masterKeyVersion string
	contextDigest    [sha256.Size]byte
	edkDigest        [sha256.Size]byte
}

// Wrapper 供 Go 加密宿主调用, 负责把认证上下文绑定到解包路径,
// 并缓存已解包的明文数据密钥。
//
// 缓存键 = (主密钥版本, 上下文摘要, EDK 摘要)。任何一项不同都会
// 绕过缓存重新走解包回调, 且回调收到的上下文与本次调用完全一致。
// 主密钥版本被禁用后, 其缓存条目立即失效并被清除。
type Wrapper struct {
	unwrapper Unwrapper
	source    MasterKeySource

	mu    sync.Mutex
	cache map[cacheKey][]byte
}

// NewWrapper 构造包装组件。unwrapper 与 source 均不可为 nil。
func NewWrapper(unwrapper Unwrapper, source MasterKeySource) (*Wrapper, error) {
	if unwrapper == nil {
		return nil, errors.New("netsec: nil unwrapper")
	}
	if source == nil {
		return nil, errors.New("netsec: nil master key source")
	}
	return &Wrapper{
		unwrapper: unwrapper,
		source:    source,
		cache:     make(map[cacheKey][]byte),
	}, nil
}

// Unwrap 解包密文数据密钥。
//
// 流程: 取当前主密钥版本并确认未禁用 -> 以 (版本, 上下文摘要, EDK 摘要)
// 查缓存 -> 未命中则调用解包回调, 回调收到的 AuthContext 与 actx 的
// 规范编码逐字节一致 -> 写入缓存。
func (w *Wrapper) Unwrap(ctx context.Context, actx Context, edk []byte) ([]byte, error) {
	canonical, err := actx.Canonical()
	if err != nil {
		return nil, err
	}
	version, err := w.source.Current()
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve master key version: %w", err)
	}
	if !w.source.Enabled(version) {
		w.evictVersion(version)
		return nil, fmt.Errorf("%w: %q", ErrMasterKeyDisabled, version)
	}

	key := cacheKey{
		masterKeyVersion: version,
		contextDigest:    sha256.Sum256(canonical),
		edkDigest:        sha256.Sum256(edk),
	}

	w.mu.Lock()
	if cached, ok := w.cache[key]; ok {
		plaintext := append([]byte(nil), cached...)
		w.mu.Unlock()
		return plaintext, nil
	}
	w.mu.Unlock()

	plaintext, err := w.unwrapper.Unwrap(ctx, UnwrapRequest{
		MasterKeyVersion: version,
		EncryptedDEK:     append([]byte(nil), edk...),
		AuthContext:      canonical,
	})
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	w.cache[key] = append([]byte(nil), plaintext...)
	w.mu.Unlock()
	return plaintext, nil
}

// evictVersion 清除指定主密钥版本下的全部缓存条目。
func (w *Wrapper) evictVersion(version string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for key := range w.cache {
		if key.masterKeyVersion == version {
			delete(w.cache, key)
		}
	}
}

// CacheSize 返回当前缓存条目数, 供观测与测试使用。
func (w *Wrapper) CacheSize() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.cache)
}
