package policycheck

import "fmt"

// Put 写入一个命名谓词，并返回可写入溯源证书的变更摘要。
func (r Registry) Put(predicate NamedPredicate) (NamedChange, error) {
	if r == nil {
		return NamedChange{}, fmt.Errorf("policycheck: nil registry")
	}
	if predicate.Name == "" {
		return NamedChange{}, fmt.Errorf("policycheck: named predicate requires a name")
	}
	change := NamedChange{Name: predicate.Name, NewDigest: predicate.ContentDigest()}
	if old, ok := r[predicate.Name]; ok {
		digest := old.ContentDigest()
		change.OldDigest = digest
	}
	r[predicate.Name] = predicate
	return change, nil
}

// IncrementalVerifier 组合注册表、DAG、HMAC 密钥和增量重算流程。
type IncrementalVerifier struct {
	Policy   *Policy
	DAG      *DAG
	Registry Registry
	Key      []byte
}

// UpdateNamedPredicate 更新注册表中的一个命名谓词，并只把溯源图中
// 受影响的节点下标交给 rebuild。
func (v *IncrementalVerifier) UpdateNamedPredicate(next NamedPredicate, rebuild BranchRebuildFunc) (*IncrementalResult, error) {
	if v == nil || v.DAG == nil {
		return nil, fmt.Errorf("policycheck: incremental verifier requires a DAG")
	}
	if v.Registry == nil {
		v.Registry = make(Registry)
	}
	change, err := v.Registry.Put(next)
	if err != nil {
		return nil, err
	}
	return RecomputeAffectedBranches(v.DAG, change, v.Key, rebuild)
}

// Verify 使用增量校验器当前的策略、DAG 和注册表执行等价校验。
func (v *IncrementalVerifier) Verify(opts *Options) (*Report, error) {
	if v == nil {
		return nil, fmt.Errorf("policycheck: nil incremental verifier")
	}
	if opts == nil {
		opts = &Options{}
	}
	if opts.Registry == nil {
		opts.Registry = v.Registry
	}
	return Verify(v.Policy, v.DAG, opts)
}
