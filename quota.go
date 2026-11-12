package netsec

import (
	"context"
	"errors"
)

var (
	// ErrQuotaExhausted 表示可信计数器已经推进到额度上限，
	// 节点再也不能签发任何产物。该状态不可通过回滚普通存储解除。
	ErrQuotaExhausted = errors.New("netsec: signing quota exhausted")

	// ErrCounterUnavailable 表示可信单调计数器无法访问，
	// 此时宿主必须拒绝服务，而不是绕过计数器继续签发。
	ErrCounterUnavailable = errors.New("netsec: trusted counter unavailable")
)

// Signer 是宿主调用的实际签名器（例如离线节点上的签名后端）。
type Signer interface {
	// Sign 对产物进行签名。实现可能因超时、取消或后端故障返回错误，
	// 此时本次已经消耗的计数不会退还。
	Sign(ctx context.Context, artifact []byte) (signature []byte, err error)
}

// Host 是供 Go 签名宿主调用的额度协议模型。
//
// 额度的唯一事实来源是注入的可信单调计数器：
// 普通磁盘上的 Store 只保存成功签发的回执，可被克隆/回滚，
// 但无法增加或恢复额度。
type Host struct {
	counter Counter
	signer  Signer
	store   Store
	limit   uint64
}

// NewHost 组装额度宿主。limit 是节点获准签发的产物总数。
func NewHost(counter Counter, signer Signer, store Store, limit uint64) *Host {
	return &Host{
		counter: counter,
		signer:  signer,
		store:   store,
		limit:   limit,
	}
}

// Remaining 返回剩余可签发数量，仅用于观测；
// 计数器不可用时返回 0 与包装后的 ErrCounterUnavailable。
func (h *Host) Remaining(ctx context.Context) (uint64, error) {
	value, err := h.counter.Value(ctx)
	if err != nil {
		return 0, errors.Join(ErrCounterUnavailable, err)
	}
	if value >= h.limit {
		return 0, nil
	}
	return h.limit - value, nil
}

// Issue 按额度协议签发一个产物，严格执行以下顺序：
//
//  1. 先在可信单调计数器上消耗一次计数（先消耗，后签发）；
//  2. 计数器推进后超过额度上限，则拒绝签发——计数已消耗且不退还；
//  3. 调用签名器；签名器超时或失败同样不退还已消耗的计数；
//  4. 仅在签名成功后，把回执追加到普通存储。
//
// 因此无论后续步骤成功与否，一次 Increment 都永久占用一个额度位置，
// 快照回滚、磁盘克隆都无法把它拿回来。
func (h *Host) Issue(ctx context.Context, artifact []byte) (Receipt, error) {
	// 第一步：先消耗计数。计数器不可用时直接拒绝服务。
	seq, err := h.counter.Increment(ctx)
	if err != nil {
		return Receipt{}, errors.Join(ErrCounterUnavailable, err)
	}

	// 第二步：额度判定只信任计数器。越界的这次推进同样不可回退。
	if seq > h.limit {
		return Receipt{}, ErrQuotaExhausted
	}

	// 第三步：调用签名器。超时/失败不退额。
	signature, err := h.signer.Sign(ctx, artifact)
	if err != nil {
		return Receipt{}, err
	}

	// 第四步：仅成功签发才落回执。
	receipt := Receipt{
		Seq:       seq,
		Artifact:  EncodeArtifact(artifact),
		Signature: EncodeArtifact(signature),
	}
	if err := h.store.Append(receipt); err != nil {
		// 计数与签名均已发生，存储故障不能让本次签发“作废”，
		// 只把错误连同回执返回给调用方，额度照常消耗。
		return receipt, err
	}
	return receipt, nil
}
