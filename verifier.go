package netsec

import (
	"errors"
	"fmt"
)

// ScopeKey 标识证明的最小覆盖单元：(作用域, 块)。
// Scope 为 GlobalScope 表示全局覆盖，否则为具体分支名。
type ScopeKey struct {
	Scope string
	Block BlockID
}

// RejectedProof 记录一条未被采信的证明及其拒证原因。
type RejectedProof struct {
	Proof  Proof
	Reason error
}

// Stats 汇总本次核验的规模与结论计数，便于宿主做审计与告警。
type Stats struct {
	Branches         int
	Targets          int
	ReachablePairs   int
	TotalPaths       int
	AcceptedProofs   int
	RejectedProofs   int
	IrrelevantProofs int
	UncoveredPairs   int
	UncoveredPaths   int
}

// CrossCheckResult 是增量合并结论与小图全遍历参考实现的对照结果。
type CrossCheckResult struct {
	Matched                bool
	IncrementalUncovered   []Path
	FullScanUncovered      []Path
	MissingFromIncremental []Path
	ExtraInIncremental     []Path
}

// VerifyReport 是一次删除作业证据核验的完整结论。
type VerifyReport struct {
	// Complete 仅当每个 (分支, 目标块) 可达引用都被匹配证明覆盖时为真。
	Complete           bool
	UncoveredPaths     []Path
	UncoveredKeys      []ScopeKey
	UnreachableTargets []BlockID
	AcceptedProofs     []Proof
	IrrelevantProofs   []Proof
	RejectedProofs     []RejectedProof
	Stats              Stats
	CrossCheck         *CrossCheckResult
}

// Verifier 是增量证明合并器：备份宿主可随作业进度持续追加证明，
// 任意时刻调用 Report/Verify 获取当前覆盖结论。非并发安全，
// 宿主多 goroutine 写入时需自行加锁。
type Verifier struct {
	dag        *DAG
	targets    BlockSet
	auth       Authenticator
	accepted   map[ScopeKey]Proof
	irrelevant map[ScopeKey]Proof
	rejected   []RejectedProof
}

// NewVerifier 校验 DAG 结构并构造增量核验器。targets 为隐私删除作业的
// 目标块引用集合；auth 为宿主指定的证明认证器。
func NewVerifier(dag *DAG, targets BlockSet, auth Authenticator) (*Verifier, error) {
	if err := ValidateDAG(dag); err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrProofInvalid)
	}
	copied := make(BlockSet, len(targets))
	for id := range targets {
		copied[id] = struct{}{}
	}
	return &Verifier{
		dag:        dag,
		targets:    copied,
		auth:       auth,
		accepted:   make(map[ScopeKey]Proof),
		irrelevant: make(map[ScopeKey]Proof),
	}, nil
}

// Add 采信一条证明。重复提交（同一 ScopeKey 且内容一致）视为幂等无操作；
// 结构非法、块/分支未知、认证失败或同键冲突都会记录到拒证列表并返回错误。
func (v *Verifier) Add(proof Proof) error {
	return ingestProof(v.dag, v.auth, v.targets, v.accepted, v.irrelevant, &v.rejected, proof)
}

// AddBatch 批量追加证明，处理完全部条目后用 errors.Join 返回所有拒证原因。
func (v *Verifier) AddBatch(proofs []Proof) error {
	var joined error
	for _, proof := range proofs {
		if err := v.Add(proof); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// Report 基于当前已合并证明生成覆盖报告，并自动与全遍历参考实现对照。
func (v *Verifier) Report() *VerifyReport {
	report := buildReport(v.dag, v.targets, v.accepted, v.irrelevant, v.rejected)
	full := FullScanWithStore(v.dag, v.targets, v.accepted, v.irrelevant, v.rejected)
	report.CrossCheck = compareReports(report, full)
	if !report.CrossCheck.Matched {
		report.Complete = false
	}
	return report
}

// Verify 是 Report 的语义化别名：返回删除作业是否可判定完成的核验结论。
func (v *Verifier) Verify() *VerifyReport {
	return v.Report()
}

// FullScan 忽略任何增量状态，对给定证明集合做一次性小图全遍历核验。
// 它作为参考实现与 Verifier 的增量合并结论交叉对照。
func FullScan(dag *DAG, targets BlockSet, auth Authenticator, proofs []Proof) (*VerifyReport, error) {
	if err := ValidateDAG(dag); err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrProofInvalid)
	}
	accepted := make(map[ScopeKey]Proof)
	irrelevant := make(map[ScopeKey]Proof)
	var rejected []RejectedProof
	var joined error
	for _, proof := range proofs {
		if err := ingestProof(dag, auth, targets, accepted, irrelevant, &rejected, proof); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return buildFullScanReport(dag, targets, accepted, irrelevant, rejected), joined
}

// FullScanWithStore 用已认证的证明存储直接做全遍历，供内部交叉对照使用。
func FullScanWithStore(dag *DAG, targets BlockSet, accepted, irrelevant map[ScopeKey]Proof, rejected []RejectedProof) *VerifyReport {
	return buildFullScanReport(dag, targets, cloneProofMap(accepted), cloneProofMap(irrelevant), cloneRejected(rejected))
}

// ingestProof 是增量合并与全遍历共用的唯一采信入口，保证两条路径
// 对非法证明、冲突与拒证的判定完全一致。
func ingestProof(dag *DAG, auth Authenticator, targets BlockSet, accepted, irrelevant map[ScopeKey]Proof, rejected *[]RejectedProof, proof Proof) error {
	reject := func(err error) error {
		*rejected = append(*rejected, RejectedProof{Proof: proof, Reason: err})
		return err
	}
	if proof.BlockID == "" {
		return reject(fmt.Errorf("%w: empty block ID", ErrProofInvalid))
	}
	if proof.Kind != KindDeletion && proof.Kind != KindUndecryptable {
		return reject(fmt.Errorf("%w: unknown proof kind %d", ErrProofInvalid, proof.Kind))
	}
	if proof.SignerID == "" {
		return reject(fmt.Errorf("%w: empty signer ID", ErrProofInvalid))
	}
	if len(proof.Signature) == 0 {
		return reject(fmt.Errorf("%w: empty signature", ErrProofInvalid))
	}
	if _, ok := dag.Blocks[proof.BlockID]; !ok {
		return reject(fmt.Errorf("%w: block %q", ErrProofUnknownBlock, proof.BlockID))
	}
	if proof.Scope != GlobalScope && !branchExists(dag, proof.Scope) {
		return reject(fmt.Errorf("%w: branch %q", ErrProofUnknownBranch, proof.Scope))
	}
	if err := auth.Verify(proof); err != nil {
		return reject(err)
	}

	key := ScopeKey{Scope: proof.Scope, Block: proof.BlockID}
	target := targets.Contains(proof.BlockID)
	if target {
		if existing, ok := accepted[key]; ok {
			if sameProof(existing, proof) {
				return nil
			}
			return reject(fmt.Errorf("%w: scope %q block %q", ErrProofConflict, proof.Scope, proof.BlockID))
		}
		if _, ok := irrelevant[key]; ok {
			return reject(fmt.Errorf("%w: scope %q block %q", ErrProofConflict, proof.Scope, proof.BlockID))
		}
		accepted[key] = proof
		return nil
	}

	if existing, ok := irrelevant[key]; ok {
		if sameProof(existing, proof) {
			return nil
		}
		return reject(fmt.Errorf("%w: scope %q block %q", ErrProofConflict, proof.Scope, proof.BlockID))
	}
	if _, ok := accepted[key]; ok {
		return reject(fmt.Errorf("%w: scope %q block %q", ErrProofConflict, proof.Scope, proof.BlockID))
	}
	irrelevant[key] = proof
	return nil
}
