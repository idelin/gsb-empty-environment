package keyusage

import (
	"fmt"
	"sync"
	"time"
)

// DenialReason 是机器可读的拒绝原因码。
type DenialReason string

const (
	// ReasonUnknownUsage 请求了系统不认识的用途。
	ReasonUnknownUsage DenialReason = "unknown_usage"
	// ReasonWeakAlgorithm 请求使用已知弱算法。
	ReasonWeakAlgorithm DenialReason = "weak_algorithm"
	// ReasonAlgorithmNotWhitelisted 算法不在白名单内。
	ReasonAlgorithmNotWhitelisted DenialReason = "algorithm_not_whitelisted"
	// ReasonAlgorithmUsageMismatch 白名单算法不支持请求的用途。
	ReasonAlgorithmUsageMismatch DenialReason = "algorithm_usage_mismatch"
	// ReasonUsageNotDeclared 密钥未声明请求的用途。
	ReasonUsageNotDeclared DenialReason = "usage_not_declared"
	// ReasonCrossFamilyUsage 签名密钥用于加解密，或加密密钥用于签名。
	ReasonCrossFamilyUsage DenialReason = "cross_family_usage"
	// ReasonWrapKeyForData 包装密钥被用于业务数据加解密。
	ReasonWrapKeyForData DenialReason = "wrap_key_for_business_data"
	// ReasonDeriveNotPermitted 父密钥未声明派生用途。
	ReasonDeriveNotPermitted DenialReason = "derive_not_permitted"
	// ReasonDerivedUsageExceedsParent 派生密钥用途超出父密钥用途子集。
	ReasonDerivedUsageExceedsParent DenialReason = "derived_usage_exceeds_parent"
	// ReasonEmptyDerivedUsages 派生密钥未声明任何用途。
	ReasonEmptyDerivedUsages DenialReason = "empty_derived_usages"
	// ReasonInvalidKey 密钥登记/派生声明本身不合法。
	ReasonInvalidKey DenialReason = "invalid_key"
)

// Event 是一次被拒绝调用的审计记录。
type Event struct {
	Time      time.Time
	KeyID     string
	Algorithm Algorithm
	Requested Usage
	Reason    DenialReason
	Detail    string
}

// Sink 接收拒绝事件。实现需保证并发安全。
type Sink interface {
	Record(Event)
}

// MemorySink 把拒绝事件保存在内存中，便于服务核验与测试断言。
type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

// NewMemorySink 创建内存审计接收器。
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Record 追加一条拒绝事件。
func (s *MemorySink) Record(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Events 返回已记录拒绝事件的副本。
func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// DenialError 是策略拒绝错误，携带原因码与审计详情。
type DenialError struct {
	Reason DenialReason
	Detail string
}

func (e *DenialError) Error() string {
	return fmt.Sprintf("keyusage: denied (%s): %s", e.Reason, e.Detail)
}

func newDenial(reason DenialReason, format string, args ...any) *DenialError {
	return &DenialError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
