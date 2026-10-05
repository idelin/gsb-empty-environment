package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Proof 是“无法达到精确人数区间”的可机器校验证明。
//
// 由于禁止拆组，目标版本可能的人数恰好等于全部组人数的子集和集合
// ReachableSums。证明由两部分组成：
//  1. 算术判据（可选）：若所有组人数的最大公约数 g > 1，则任何可达人数
//     都是 g 的倍数；若区间 [Lo,Hi] 内没有 g 的倍数，即一行证明。
//  2. 完备枚举判据：可达人数集合与区间不相交。证书携带完整的有序可达
//     集合及其 SHA-256 摘要，校验方由输入独立重算并比对，同时检查区间
//     与集合不相交、以及前驱/后继值正确。
type Proof struct {
	Type          string  `json:"type"` // "infeasibility-proof"
	Lo            int64   `json:"lo"`
	Hi            int64   `json:"hi"`
	TotalUsers    int64   `json:"total_users"`
	GCD           int64   `json:"gcd"`
	ArithProof    bool    `json:"arith_proof"` // gcd 判据是否已足够
	Predecessor   int64   `json:"predecessor"` // 最大的可达人数 < Lo（不存在为 -1）
	Successor     int64   `json:"successor"`   // 最小的可达人数 > Hi（不存在为 -1）
	ReachableCnt  int     `json:"reachable_count"`
	ReachableSums []int64 `json:"reachable_sums"`
	SHA256        string  `json:"sha256"` // 可达集合规范编码的摘要
}

// BuildProof 由输入与目标区间构造不可达证明。调用前需确认无可行解。
func BuildProof(groups []Group, iv Interval, reachable []int64) *Proof {
	p := &Proof{
		Type:          "infeasibility-proof",
		Lo:            iv.Lo,
		Hi:            iv.Hi,
		TotalUsers:    TotalUsers(groups),
		GCD:           GCD(groups),
		Predecessor:   -1,
		Successor:     -1,
		ReachableSums: reachable,
		ReachableCnt:  len(reachable),
		SHA256:        digestSums(reachable),
	}
	// 前驱：最后一个 < Lo 的可达值；后继：第一个 > Hi 的可达值。
	for _, s := range reachable {
		if s < iv.Lo {
			p.Predecessor = s
		}
		if s > iv.Hi {
			p.Successor = s
			break
		}
	}
	// 算术判据：区间内不存在 gcd 的倍数时，gcd 论证本身即构成完整证明。
	if p.GCD > 1 && iv.Hi/p.GCD < (iv.Lo+p.GCD-1)/p.GCD {
		p.ArithProof = true
	}
	return p
}

// VerifyProof 独立校验不可达证明：
//  1. 由输入重新计算可达人数集合，与证书中的集合及摘要一致；
//  2. 可达集合与区间 [Lo,Hi] 不相交；
//  3. 前驱/后继与重算结果一致；
//  4. 若声明了算术判据，复核 gcd 论证成立。
func VerifyProof(groups []Group, p *Proof) error {
	if p.Type != "infeasibility-proof" {
		return errors.New("not an infeasibility proof")
	}
	if p.Lo > p.Hi {
		return errors.New("proof has empty interval")
	}
	recomputed := ReachableSums(groups)
	if len(recomputed) != len(p.ReachableSums) {
		return fmt.Errorf("reachable set size mismatch: recomputed %d, proof %d",
			len(recomputed), len(p.ReachableSums))
	}
	for i := range recomputed {
		if recomputed[i] != p.ReachableSums[i] {
			return fmt.Errorf("reachable set mismatch at index %d: recomputed %d, proof %d",
				i, recomputed[i], p.ReachableSums[i])
		}
	}
	if digestSums(recomputed) != p.SHA256 {
		return errors.New("sha256 of reachable set mismatch")
	}
	for _, s := range recomputed {
		if s >= p.Lo && s <= p.Hi {
			return fmt.Errorf("proof invalid: %d is reachable and inside [%d,%d]", s, p.Lo, p.Hi)
		}
	}
	pred, succ := int64(-1), int64(-1)
	for _, s := range recomputed {
		if s < p.Lo {
			pred = s
		}
		if s > p.Hi {
			succ = s
			break
		}
	}
	if pred != p.Predecessor || succ != p.Successor {
		return fmt.Errorf("predecessor/successor mismatch: recomputed (%d,%d), proof (%d,%d)",
			pred, succ, p.Predecessor, p.Successor)
	}
	if p.ArithProof {
		g := GCD(groups)
		if g <= 1 || g != p.GCD {
			return errors.New("arith proof claimed but gcd does not match")
		}
		if p.Hi/g >= (p.Lo+g-1)/g {
			return errors.New("arith proof claimed but interval contains a multiple of gcd")
		}
	}
	return nil
}

// digestSums 计算有序可达集合的规范摘要（小端 int64 序列）。
func digestSums(sums []int64) string {
	h := sha256.New()
	var buf [8]byte
	for _, s := range sums {
		binary.LittleEndian.PutUint64(buf[:], uint64(s))
		h.Write(buf[:])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
