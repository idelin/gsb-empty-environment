package keyusage

import (
	"sort"
	"sync"
	"time"
)

// Enforcer 是密钥用途约束组件的核心：所有密钥调用都必须先通过 Check，
// 每次拒绝都会写入审计 Sink。它内部维护已登记密钥及其派生关系。
type Enforcer struct {
	mu   sync.RWMutex
	keys map[string]Key
	sink Sink
	now  func() time.Time
}

// NewEnforcer 创建执行器。sink 为 nil 时使用 MemorySink。
func NewEnforcer(sink Sink) *Enforcer {
	if sink == nil {
		sink = NewMemorySink()
	}
	return &Enforcer{
		keys: make(map[string]Key),
		sink: sink,
		now:  time.Now,
	}
}

// Register 登记一把根密钥或已构造好的派生密钥，并校验其声明合法性。
// 派生密钥（ParentID 非空）的用途只能是父密钥用途的子集。
func (e *Enforcer) Register(k Key) error {
	if err := k.Validate(); err != nil {
		return e.reject(k, "", ReasonInvalidKey, err.Error())
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.keys[k.ID]; exists {
		return e.rejectLocked(k, "", ReasonInvalidKey, "key id already registered: "+k.ID)
	}

	if k.IsDerived() {
		parent, ok := e.keys[k.ParentID]
		if !ok {
			return e.rejectLocked(k, "", ReasonInvalidKey,
				"parent key %q is not registered", k.ParentID)
		}
		if err := validateDerivedUsages(k, parent); err != nil {
			return e.rejectLocked(k, "", err.Reason, err.Detail)
		}
	}

	e.keys[k.ID] = k
	return nil
}

// Derive 从父密钥派生子密钥。
// 父密钥必须声明 derive 用途且其算法支持派生；
// 子密钥的声明用途必须是父密钥用途的非空子集。
func (e *Enforcer) Derive(parentID, childID string, childAlg Algorithm, childUsages []Usage) (Key, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	parent, ok := e.keys[parentID]
	if !ok {
		return Key{}, e.rejectLocked(Key{ID: childID, Algorithm: childAlg, ParentID: parentID},
			UsageDerive, ReasonInvalidKey, "parent key %q is not registered", parentID)
	}

	child := Key{
		ID:        childID,
		Algorithm: childAlg,
		Usages:    append([]Usage(nil), childUsages...),
		ParentID:  parentID,
	}

	if len(child.Usages) == 0 {
		return Key{}, e.rejectLocked(child, UsageDerive, ReasonEmptyDerivedUsages,
			"derived key %q declares no usages", childID)
	}

	if err := parent.Validate(); err != nil {
		return Key{}, e.rejectLocked(parent, UsageDerive, ReasonInvalidKey, err.Error())
	}
	if !parent.Declares(UsageDerive) {
		return Key{}, e.rejectLocked(parent, UsageDerive, ReasonDeriveNotPermitted,
			"parent key %q does not declare derive usage", parentID)
	}
	if !algorithmAllows(parent.Algorithm, UsageDerive) {
		return Key{}, e.rejectLocked(parent, UsageDerive, ReasonAlgorithmUsageMismatch,
			"parent algorithm %q does not support derivation", parent.Algorithm)
	}

	// 子集约束优先：派生密钥的用途只能是父用途的子集。
	if err := validateDerivedUsages(child, parent); err != nil {
		return Key{}, e.rejectLocked(child, UsageDerive, err.Reason, err.Detail)
	}

	if err := child.Validate(); err != nil {
		return Key{}, e.rejectLocked(child, UsageDerive, ReasonInvalidKey, err.Error())
	}
	if _, exists := e.keys[childID]; exists {
		return Key{}, e.rejectLocked(child, UsageDerive, ReasonInvalidKey,
			"key id already registered: "+childID)
	}

	e.keys[childID] = child
	return child, nil
}

// Check 是每次密钥调用前的强制关卡：
// 通过则返回 nil，拒绝则返回 *DenialError 并同步写入审计记录。
func (e *Enforcer) Check(k Key, requested Usage) error {
	if !IsKnownUsage(requested) {
		return e.reject(k, requested, ReasonUnknownUsage,
			"unknown requested usage %q", requested)
	}

	if IsWeak(k.Algorithm) {
		return e.reject(k, requested, ReasonWeakAlgorithm,
			"weak algorithm %q is prohibited", k.Algorithm)
	}
	if !IsWhitelisted(k.Algorithm) {
		return e.reject(k, requested, ReasonAlgorithmNotWhitelisted,
			"algorithm %q is not whitelisted", k.Algorithm)
	}

	if crossFamilyViolation(k, requested) {
		return e.reject(k, requested, ReasonCrossFamilyUsage,
			"signing key cannot be used for confidentiality or vice versa on key %q", k.ID)
	}

	if isWrapOnlyKey(k) && (requested == UsageEncrypt || requested == UsageDecrypt) {
		return e.reject(k, requested, ReasonWrapKeyForData,
			"wrap-only key %q must not process business data", k.ID)
	}

	if !algorithmAllows(k.Algorithm, requested) {
		return e.reject(k, requested, ReasonAlgorithmUsageMismatch,
			"algorithm %q does not support usage %q", k.Algorithm, requested)
	}

	if !k.Declares(requested) {
		return e.reject(k, requested, ReasonUsageNotDeclared,
			"key %q does not declare usage %q", k.ID, requested)
	}
	return nil
}

// Key 返回已登记密钥的副本。
func (e *Enforcer) Key(id string) (Key, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	k, ok := e.keys[id]
	return k, ok
}

// crossFamilyViolation 判断请求是否跨越签名/保密用途族。
func crossFamilyViolation(k Key, requested Usage) bool {
	for _, u := range k.Usages {
		if IsSigningUsage(u) && IsConfidentialityUsage(requested) {
			return true
		}
		if IsConfidentialityUsage(u) && IsSigningUsage(requested) {
			return true
		}
	}
	return false
}

// isWrapOnlyKey 报告密钥是否仅声明包装/解包用途（包装密钥）。
func isWrapOnlyKey(k Key) bool {
	hasWrap := false
	for _, u := range k.Usages {
		switch u {
		case UsageWrapKey, UsageUnwrapKey:
			hasWrap = true
		case UsageEncrypt, UsageDecrypt:
			return false
		}
	}
	return hasWrap
}

// validateDerivedUsages 强制派生密钥只继承父用途的子集。
func validateDerivedUsages(child, parent Key) *DenialError {
	if len(child.Usages) == 0 {
		return newDenial(ReasonEmptyDerivedUsages,
			"derived key %q declares no usages", child.ID)
	}

	parentDeclared := make(map[Usage]bool, len(parent.Usages))
	for _, u := range parent.Usages {
		parentDeclared[u] = true
	}

	var exceeded []Usage
	for _, u := range child.Usages {
		if !parentDeclared[u] {
			exceeded = append(exceeded, u)
		}
	}
	if len(exceeded) > 0 {
		sort.Slice(exceeded, func(i, j int) bool { return exceeded[i] < exceeded[j] })
		return newDenial(ReasonDerivedUsageExceedsParent,
			"derived key %q usages %v exceed parent %q usages", child.ID, exceeded, parent.ID)
	}
	return nil
}

func (e *Enforcer) reject(k Key, requested Usage, reason DenialReason, format string, args ...any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rejectLocked(k, requested, reason, format, args...)
}

// rejectLocked 调用时调用方已持有写锁（或无需与 map 操作互斥的构造期场景）。
func (e *Enforcer) rejectLocked(k Key, requested Usage, reason DenialReason, format string, args ...any) error {
	denied := newDenial(reason, format, args...)
	e.sink.Record(Event{
		Time:      e.now(),
		KeyID:     k.ID,
		Algorithm: k.Algorithm,
		Requested: requested,
		Reason:    reason,
		Detail:    denied.Detail,
	})
	return denied
}
