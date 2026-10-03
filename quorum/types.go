// Package quorum 实现分层仲裁多数派决策引擎:
// 多数据中心分级 Quorum 仲裁,支持区域权重、本地多数派门限、
// 主中心失联时的降级仲裁拓扑,以及携带 HMAC 签名证据的仲裁结果。
package quorum

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// Mode 仲裁拓扑模式。
type Mode int

const (
	// ModeNormal 正常拓扑:主中心可达,按全局权重多数派仲裁。
	ModeNormal Mode = iota
	// ModeDegraded 降级拓扑:主中心不可达,存活区域按局部多数派临时仲裁。
	ModeDegraded
)

func (m Mode) String() string {
	if m == ModeDegraded {
		return "DEGRADED"
	}
	return "NORMAL"
}

// Region 一个数据中心(区域)的仲裁配置。
type Region struct {
	ID string // 区域标识,如 "IDC-A"
	// Weight 该区域在全局仲裁中的权重。
	Weight int
	// LocalNodes 区域内参与本地多数派的节点数。
	LocalNodes int
	// LocalThreshold 区域投票生效所需的本地多数派门限(本地赞成票数下限)。
	LocalThreshold int
	// Key 区域签名密钥(HMAC-SHA256),用于签发与验证投票证据。
	Key []byte
}

// Config 仲裁器配置。
type Config struct {
	Regions []Region
	// Primary 主中心区域 ID;其不可达时触发降级仲裁拓扑。
	Primary string
}

// Vote 单个区域对某提案的一次签名投票。
type Vote struct {
	RegionID   string
	Epoch      uint64
	Proposal   string
	Approve    bool
	LocalVotes int // 该区域本地多数派实际赞成票数
	Signature  string
}

// canonical 投票的规范化签名载荷。
func (v Vote) canonical() string {
	approve := "0"
	if v.Approve {
		approve = "1"
	}
	return fmt.Sprintf("vote|%s|%d|%s|%s|%d", v.RegionID, v.Epoch, v.Proposal, approve, v.LocalVotes)
}

// sign 使用区域密钥对投票签名。
func (r Region) sign(v Vote) string {
	mac := hmac.New(sha256.New, r.Key)
	mac.Write([]byte(v.canonical()))
	return hex.EncodeToString(mac.Sum(nil))
}

// verify 校验投票签名是否由该区域密钥签发。
func (r Region) verify(v Vote) bool {
	return hmac.Equal([]byte(r.sign(v)), []byte(v.Signature))
}

// Decision 一次仲裁结果,携带完整签名证据(所有投票)。
type Decision struct {
	Proposal string
	Epoch    uint64
	Mode     Mode
	Votes    []Vote // 签名证据:参与本次仲裁的全部区域投票
	Weight   int    // 赞成票权重合计
	Quorum   int    // 本次拓扑下的多数派门限
	Decided  bool   // 是否达成多数派
}

// 错误定义。
var (
	ErrNoQuorum      = errors.New("quorum: 未达成多数派")
	ErrStaleEpoch    = errors.New("quorum: 纪元编号过低,存在更新的仲裁状态")
	ErrUnknownRegion = errors.New("quorum: 未知区域")
	ErrPrimaryAlive  = errors.New("quorum: 主中心可达,禁止进入降级拓扑")
	ErrConflict      = errors.New("quorum: 状态冲突,同一纪元的已提交决议不一致")
)

// majority 返回权重 w 的严格多数派门限。
func majority(w int) int { return w/2 + 1 }

// totalWeight 配置中全部区域权重之和。
func (c Config) totalWeight() int {
	total := 0
	for _, r := range c.Regions {
		total += r.Weight
	}
	return total
}

// region 按 ID 查找区域。
func (c Config) region(id string) (Region, bool) {
	for _, r := range c.Regions {
		if r.ID == id {
			return r, true
		}
	}
	return Region{}, false
}

// VerifyDecision 独立验证一份仲裁结果:
// 重放签名证据、核对本地门限、重新计算权重与多数派门限。
// 可用于第三方(如恢复后的主中心)审计决议合法性。
func VerifyDecision(cfg Config, d Decision) error {
	if !d.Decided {
		return ErrNoQuorum
	}
	weight := 0
	seen := map[string]bool{}
	for _, v := range d.Votes {
		if !v.Approve {
			continue
		}
		r, ok := cfg.region(v.RegionID)
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownRegion, v.RegionID)
		}
		if seen[v.RegionID] {
			return fmt.Errorf("quorum: 区域 %s 重复投票", v.RegionID)
		}
		seen[v.RegionID] = true
		if v.Epoch != d.Epoch || v.Proposal != d.Proposal {
			return fmt.Errorf("quorum: 投票与决议的纪元/提案不一致")
		}
		if !r.verify(v) {
			return fmt.Errorf("quorum: 区域 %s 签名证据校验失败", v.RegionID)
		}
		if v.LocalVotes < r.LocalThreshold {
			return fmt.Errorf("quorum: 区域 %s 未达本地多数派门限(%d/%d)",
				v.RegionID, v.LocalVotes, r.LocalThreshold)
		}
		weight += r.Weight
	}
	if weight != d.Weight {
		return fmt.Errorf("quorum: 权重合计不符,期望 %d 实得 %d", d.Weight, weight)
	}
	if weight < d.Quorum {
		return fmt.Errorf("%w: 权重 %d < 门限 %d", ErrNoQuorum, weight, d.Quorum)
	}
	return nil
}

// sortedVotes 按区域 ID 排序投票,保证证据序列化确定性。
func sortedVotes(votes []Vote) []Vote {
	out := make([]Vote, len(votes))
	copy(out, votes)
	sort.Slice(out, func(i, j int) bool { return out[i].RegionID < out[j].RegionID })
	return out
}
