// Package quorum 实现分层仲裁多数派决策引擎。
//
// 多数据中心分级 Quorum 仲裁：
//   - 每个区域(IDC)拥有独立权重与本地多数派门限；
//   - 正常拓扑下按全局权重多数派裁决；
//   - 主中心不可达时动态降级为降级仲裁拓扑，剩余区域按自身权重
//     构成临时多数派，避免跨区网络闪断引起全局不可用；
//   - 主中心恢复后须出示更高的纪元编号(epoch)重新接管；
//   - 仲裁结果携带完整签名证据(HMAC-SHA256)，可独立验证。
package quorum

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Mode 表示当前仲裁拓扑。
type Mode int

const (
	// ModeNormal 正常拓扑：所有区域参与，按全局权重多数派裁决。
	ModeNormal Mode = iota
	// ModeDegraded 降级拓扑：主中心不可达，剩余可达区域构成临时多数派。
	ModeDegraded
)

func (m Mode) String() string {
	if m == ModeDegraded {
		return "degraded"
	}
	return "normal"
}

var (
	ErrUnknownRegion   = errors.New("quorum: unknown region")
	ErrRegionDown      = errors.New("quorum: voter region unreachable")
	ErrNoLocalQuorum   = errors.New("quorum: voter region local majority not met")
	ErrNoQuorum        = errors.New("quorum: weighted majority not reached")
	ErrStaleEpoch      = errors.New("quorum: stale epoch (fenced)")
	ErrConflict        = errors.New("quorum: conflicting value already decided in epoch")
	ErrPrimaryDown     = errors.New("quorum: primary unreachable, takeover rejected")
	ErrBadEvidence     = errors.New("quorum: decision evidence verification failed")
	ErrDuplicateVoter  = errors.New("quorum: duplicate voter")
	ErrPrimaryDegraded = errors.New("quorum: primary cannot vote in degraded topology")
)

// Region 描述一个数据中心的分层仲裁属性。
type Region struct {
	ID     string
	Weight int // 全局仲裁权重
	// LocalThreshold 本地多数派门限：区域内至少收到这么多本地确认，
	// 该区域的投票才有效(分层仲裁的"下层")。
	LocalThreshold int
	Primary        bool // 是否主中心

	reachable bool
	localAcks int
	key       []byte // 区域签名密钥
}

// Vote 是单个区域对某次提案的签名投票。
type Vote struct {
	RegionID  string
	Epoch     uint64
	Value     string
	Mode      Mode
	Signature []byte // HMAC-SHA256(regionKey, canonical)
}

// Decision 是仲裁结果，携带完整签名证据。
type Decision struct {
	Epoch uint64
	Value string
	Mode  Mode
	Votes []Vote
	// Evidence 为仲裁器对整份决议的签名，覆盖 epoch/value/mode/全部投票。
	Evidence []byte
}

// TotalWeight 返回本次决议的投票权重之和。
func (d *Decision) TotalWeight(regions map[string]Region) int {
	total := 0
	for _, v := range d.Votes {
		if r, ok := regions[v.RegionID]; ok {
			total += r.Weight
		}
	}
	return total
}

// Arbiter 是分层仲裁多数派决策引擎。
type Arbiter struct {
	mu          sync.Mutex
	regions     map[string]*Region
	epoch       uint64 // 当前已接受的最高纪元
	mode        Mode
	decided     map[uint64]string // epoch -> 已决值，用于冲突检测
	evidenceKey []byte            // 仲裁器证据签名密钥
}

// NewArbiter 创建仲裁器。evidenceKey 用于签发决议证据。
func NewArbiter(evidenceKey []byte) *Arbiter {
	return &Arbiter{
		regions:     make(map[string]*Region),
		mode:        ModeNormal,
		decided:     make(map[uint64]string),
		evidenceKey: append([]byte(nil), evidenceKey...),
	}
}

// AddRegion 注册区域及其签名密钥。新区域默认可达。
func (a *Arbiter) AddRegion(r Region, key []byte) error {
	if r.Weight <= 0 {
		return fmt.Errorf("quorum: region %s weight must be positive", r.ID)
	}
	if _, ok := a.regions[r.ID]; ok {
		return fmt.Errorf("quorum: region %s already registered", r.ID)
	}
	r.reachable = true
	r.key = append([]byte(nil), key...)
	a.regions[r.ID] = &r
	return nil
}

// ReportLocalAcks 上报区域内本地确认数(分层仲裁下层输入)。
func (a *Arbiter) ReportLocalAcks(id string, acks int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.regions[id]
	if !ok {
		return ErrUnknownRegion
	}
	r.localAcks = acks
	return nil
}

// SetReachable 更新区域可达性。主中心失联时自动降级为降级仲裁拓扑；
// 主中心恢复可达时不会自动接管，须通过 Takeover 出示更高纪元重新接管。
func (a *Arbiter) SetReachable(id string, reachable bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.regions[id]
	if !ok {
		return ErrUnknownRegion
	}
	r.reachable = reachable
	if r.Primary && !reachable && a.mode == ModeNormal {
		a.mode = ModeDegraded
	}
	return nil
}

// Takeover 主中心恢复后出示更高纪元重新接管，恢复正常拓扑。
func (a *Arbiter) Takeover(epoch uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.primary()
	if p == nil {
		return errors.New("quorum: no primary registered")
	}
	if !p.reachable {
		return ErrPrimaryDown
	}
	if epoch <= a.epoch {
		return ErrStaleEpoch
	}
	a.epoch = epoch
	a.mode = ModeNormal
	return nil
}

// Mode 返回当前仲裁拓扑。
func (a *Arbiter) Mode() Mode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// Epoch 返回当前纪元。
func (a *Arbiter) Epoch() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.epoch
}

func (a *Arbiter) primary() *Region {
	for _, r := range a.regions {
		if r.Primary {
			return r
		}
	}
	return nil
}

// participants 返回当前拓扑下参与仲裁的区域。
func (a *Arbiter) participants() []*Region {
	var out []*Region
	for _, r := range a.regions {
		if !r.reachable {
			continue
		}
		if a.mode == ModeDegraded && r.Primary {
			continue // 降级拓扑中主中心被隔离
		}
		out = append(out, r)
	}
	return out
}

// quorumThreshold 计算当前拓扑的多数派门限(权重过半)。
func quorumThreshold(members []*Region) int {
	total := 0
	for _, r := range members {
		total += r.Weight
	}
	return total/2 + 1
}

func sign(key []byte, parts ...string) []byte {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		var ln [4]byte
		binary.BigEndian.PutUint32(ln[:], uint32(len(p)))
		h.Write(ln[:])
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

func signVote(key []byte, regionID string, epoch uint64, value string, mode Mode) []byte {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	m := "normal"
	if mode == ModeDegraded {
		m = "degraded"
	}
	return sign(key, regionID, string(eb[:]), value, m)
}

func (a *Arbiter) signEvidence(d *Decision) []byte {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], d.Epoch)
	parts := []string{string(eb[:]), d.Value, d.Mode.String()}
	ids := make([]string, 0, len(d.Votes))
	for _, v := range d.Votes {
		ids = append(ids, v.RegionID+":"+string(v.Signature))
	}
	sort.Strings(ids)
	parts = append(parts, ids...)
	return sign(a.evidenceKey, parts...)
}

// Propose 在当前拓扑下对 value 发起仲裁。voterIDs 为投票区域。
// 成功时返回携带完整签名证据的决议。
func (a *Arbiter) Propose(epoch uint64, value string, voterIDs []string) (*Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if epoch < a.epoch {
		return nil, fmt.Errorf("%w: epoch %d < current %d", ErrStaleEpoch, epoch, a.epoch)
	}
	if prev, ok := a.decided[epoch]; ok && prev != value {
		return nil, fmt.Errorf("%w: epoch %d already decided %q", ErrConflict, epoch, prev)
	}

	seen := make(map[string]bool)
	weight := 0
	votes := make([]Vote, 0, len(voterIDs))
	for _, id := range voterIDs {
		if seen[id] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateVoter, id)
		}
		seen[id] = true
		r, ok := a.regions[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownRegion, id)
		}
		if !r.reachable {
			return nil, fmt.Errorf("%w: %s", ErrRegionDown, id)
		}
		if a.mode == ModeDegraded && r.Primary {
			return nil, fmt.Errorf("%w: %s", ErrPrimaryDegraded, id)
		}
		if r.localAcks < r.LocalThreshold {
			return nil, fmt.Errorf("%w: %s (%d/%d)", ErrNoLocalQuorum, id, r.localAcks, r.LocalThreshold)
		}
		weight += r.Weight
		votes = append(votes, Vote{
			RegionID:  id,
			Epoch:     epoch,
			Value:     value,
			Mode:      a.mode,
			Signature: signVote(r.key, id, epoch, value, a.mode),
		})
	}

	threshold := quorumThreshold(a.participants())
	if weight < threshold {
		return nil, fmt.Errorf("%w: weight %d < threshold %d (%s topology)",
			ErrNoQuorum, weight, threshold, a.mode)
	}

	d := &Decision{
		Epoch: epoch,
		Value: value,
		Mode:  a.mode,
		Votes: votes,
	}
	d.Evidence = a.signEvidence(d)

	a.decided[epoch] = value
	if epoch > a.epoch {
		a.epoch = epoch
	}
	return d, nil
}

// Snapshot 返回区域状态副本，供验证与展示使用。
func (a *Arbiter) Snapshot() map[string]Region {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]Region, len(a.regions))
	for id, r := range a.regions {
		cp := *r
		cp.key = nil
		out[id] = cp
	}
	return out
}

// Verify 独立验证决议：投票签名、拓扑门限与证据签名全部校验。
// regionKeys 为各区域签名密钥，evidenceKey 为仲裁器证据密钥。
func Verify(d *Decision, regions map[string]Region, regionKeys map[string][]byte, evidenceKey []byte) error {
	weight := 0
	var members []*Region
	for id, r := range regions {
		r := r
		if d.Mode == ModeDegraded && r.Primary {
			continue
		}
		members = append(members, &r)
		_ = id
	}
	seen := make(map[string]bool)
	for _, v := range d.Votes {
		if seen[v.RegionID] {
			return fmt.Errorf("%w: duplicate voter %s", ErrBadEvidence, v.RegionID)
		}
		seen[v.RegionID] = true
		r, ok := regions[v.RegionID]
		if !ok {
			return fmt.Errorf("%w: unknown voter %s", ErrBadEvidence, v.RegionID)
		}
		if v.Epoch != d.Epoch || v.Value != d.Value || v.Mode != d.Mode {
			return fmt.Errorf("%w: vote fields mismatch from %s", ErrBadEvidence, v.RegionID)
		}
		key, ok := regionKeys[v.RegionID]
		if !ok {
			return fmt.Errorf("%w: missing key for %s", ErrBadEvidence, v.RegionID)
		}
		want := signVote(key, v.RegionID, v.Epoch, v.Value, v.Mode)
		if !hmac.Equal(want, v.Signature) {
			return fmt.Errorf("%w: bad vote signature from %s", ErrBadEvidence, v.RegionID)
		}
		weight += r.Weight
	}
	if weight < quorumThreshold(members) {
		return fmt.Errorf("%w: quorum not satisfied", ErrBadEvidence)
	}
	// 重算证据签名
	tmp := &Arbiter{evidenceKey: evidenceKey}
	if !hmac.Equal(tmp.signEvidence(d), d.Evidence) {
		return fmt.Errorf("%w: evidence signature mismatch", ErrBadEvidence)
	}
	return nil
}
