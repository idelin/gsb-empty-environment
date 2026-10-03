package quorum

import "fmt"

// Arbiter 分层仲裁多数派决策引擎。
//
// 仲裁规则:
//   - 正常拓扑(主中心可达):全局权重严格多数派。
//   - 降级拓扑(主中心不可达):存活区域构成临时多数派。
//     降级门限基于"全部非主中心区域权重"静态计算,因此单一区域
//     在网络分区中无法自决(防脑裂),而多数存活区域可继续服务,
//     避免跨区网络闪断引起全局不可用。
//   - 降级决议抬升纪元编号,对旧主中心形成围栏(fencing);
//     主中心恢复后须出示更高的纪元编号方可重新接管,
//     旧纪元的决议一律被拒绝,保证无状态冲突。
type Arbiter struct {
	cfg       Config
	reachable map[string]bool
	epoch     uint64
	// committed 已提交决议日志(纪元单调不减)。
	committed []Decision
	// byProposal 提案 -> 已提交决议,用于冲突检测。
	byProposal map[string]Decision
}

// NewArbiter 创建仲裁器,初始所有区域可达、纪元为 1。
func NewArbiter(cfg Config) (*Arbiter, error) {
	if _, ok := cfg.region(cfg.Primary); !ok {
		return nil, fmt.Errorf("%w: 主中心 %s", ErrUnknownRegion, cfg.Primary)
	}
	for _, r := range cfg.Regions {
		if r.Weight <= 0 || r.LocalThreshold <= 0 || r.LocalThreshold > r.LocalNodes {
			return nil, fmt.Errorf("quorum: 区域 %s 配置非法(权重/本地门限)", r.ID)
		}
		if len(r.Key) == 0 {
			return nil, fmt.Errorf("quorum: 区域 %s 缺少签名密钥", r.ID)
		}
	}
	a := &Arbiter{
		cfg:        cfg,
		reachable:  map[string]bool{},
		epoch:      1,
		byProposal: map[string]Decision{},
	}
	for _, r := range cfg.Regions {
		a.reachable[r.ID] = true
	}
	return a, nil
}

// Epoch 当前纪元编号。
func (a *Arbiter) Epoch() uint64 { return a.epoch }

// Mode 当前仲裁拓扑模式。
func (a *Arbiter) Mode() Mode {
	if a.reachable[a.cfg.Primary] {
		return ModeNormal
	}
	return ModeDegraded
}

// SetReachable 更新区域可达性(模拟网络闪断/恢复)。
func (a *Arbiter) SetReachable(regionID string, reachable bool) error {
	if _, ok := a.cfg.region(regionID); !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRegion, regionID)
	}
	a.reachable[regionID] = reachable
	return nil
}

// quorumThreshold 当前拓扑下的多数派门限与可参与区域。
func (a *Arbiter) quorumThreshold() (quorum int, participants []Region) {
	if a.Mode() == ModeNormal {
		for _, r := range a.cfg.Regions {
			participants = append(participants, r)
		}
		return majority(a.cfg.totalWeight()), participants
	}
	// 降级拓扑:门限基于全部非主中心区域权重(静态,防脑裂);
	// 仅可达区域实际参与计票。
	nonPrimaryWeight := 0
	for _, r := range a.cfg.Regions {
		if r.ID == a.cfg.Primary {
			continue
		}
		nonPrimaryWeight += r.Weight
		if a.reachable[r.ID] {
			participants = append(participants, r)
		}
	}
	return majority(nonPrimaryWeight), participants
}

// Propose 对提案发起仲裁。收集各可达区域的签名投票,
// 达成多数派则提交决议并返回携带签名证据的 Decision。
// 降级拓扑下达成决议会抬升纪元,对失联主中心形成围栏。
func (a *Arbiter) Propose(proposal string) (Decision, error) {
	mode := a.Mode()
	epoch := a.epoch
	if mode == ModeDegraded {
		// 围栏:降级决议必须处于新纪元,使旧主中心持有的纪元失效。
		epoch = a.epoch + 1
	}
	quorum, participants := a.quorumThreshold()

	votes := make([]Vote, 0, len(participants))
	weight := 0
	for _, r := range participants {
		v := Vote{
			RegionID:   r.ID,
			Epoch:      epoch,
			Proposal:   proposal,
			Approve:    true,
			LocalVotes: r.LocalNodes, // 可达区域本地节点全部参与
		}
		v.Signature = r.sign(v)
		if v.LocalVotes >= r.LocalThreshold {
			weight += r.Weight
		}
		votes = append(votes, v)
	}

	d := Decision{
		Proposal: proposal,
		Epoch:    epoch,
		Mode:     mode,
		Votes:    sortedVotes(votes),
		Weight:   weight,
		Quorum:   quorum,
		Decided:  weight >= quorum,
	}
	if !d.Decided {
		return d, fmt.Errorf("%w: 提案 %q 权重 %d < 门限 %d",
			ErrNoQuorum, proposal, weight, quorum)
	}
	if err := a.commit(d); err != nil {
		return d, err
	}
	return d, nil
}

// commit 提交决议:校验纪元单调性与提案一致性,防止状态冲突。
func (a *Arbiter) commit(d Decision) error {
	if d.Epoch < a.epoch {
		return fmt.Errorf("%w: 决议纪元 %d < 当前纪元 %d", ErrStaleEpoch, d.Epoch, a.epoch)
	}
	if prev, ok := a.byProposal[d.Proposal]; ok && prev.Epoch == d.Epoch {
		return fmt.Errorf("%w: 提案 %q 在纪元 %d 已提交", ErrConflict, d.Proposal, d.Epoch)
	}
	a.epoch = d.Epoch
	a.committed = append(a.committed, d)
	a.byProposal[d.Proposal] = d
	return nil
}

// RestorePrimary 主中心恢复后重新接管仲裁权。
// newEpoch 必须严格大于当前纪元(即出示更高的纪元编号),
// 否则说明主中心持有的是过期状态,拒绝接管以避免状态冲突。
func (a *Arbiter) RestorePrimary(newEpoch uint64) error {
	if !a.reachable[a.cfg.Primary] {
		return fmt.Errorf("quorum: 主中心 %s 仍不可达", a.cfg.Primary)
	}
	if newEpoch <= a.epoch {
		return fmt.Errorf("%w: 接管纪元 %d <= 当前纪元 %d", ErrStaleEpoch, newEpoch, a.epoch)
	}
	a.epoch = newEpoch
	return nil
}

// Committed 返回已提交决议日志(纪元单调不减)。
func (a *Arbiter) Committed() []Decision {
	out := make([]Decision, len(a.committed))
	copy(out, a.committed)
	return out
}
