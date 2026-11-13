package netsec

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// MaxModulus 是调用方允许的最大模数（奇素数上限）。
const MaxModulus = 65521

// ErrBudget 表示轨迹步数超过调用方给定预算。
var ErrBudget = errors.New("netsec: trace budget exceeded")

// 轨迹符号定义：
//
//	A: u 为偶，u /= 2
//	B: v 为偶，v /= 2
//	C: u > v，u = (u-v)/2
//	D: 否则，v = (v-u)/2
const (
	StepA = 'A'
	StepB = 'B'
	StepC = 'C'
	StepD = 'D'
)

// ValidateParams 校验模数与秘密标量的基本约束。
// p 必须是满足 3 <= p <= MaxModulus 的奇数（素性由调用方保证），
// x 必须落在 [1, p-1]。
func ValidateParams(x, p uint64) error {
	if p < 3 || p > MaxModulus || p%2 == 0 {
		return fmt.Errorf("netsec: modulus %d is not an odd prime candidate in [3, %d]", p, MaxModulus)
	}
	if x == 0 || x >= p {
		return fmt.Errorf("netsec: secret %d out of range [1, %d]", x, p-1)
	}
	return nil
}

// Rounds 返回加固实现的固定轮数 2*bitlen(p)+1。
func Rounds(p uint64) int {
	return 2*bits.Len64(p) + 1
}

// halfMod 计算 (t/2) mod p，要求 t < p。
// 若 t 为奇数则先加 p 再折半，结果仍小于 p。
func halfMod(t, p uint64) uint64 {
	if t&1 == 1 {
		t += p
	}
	return t >> 1
}

// subMod 计算 (a-b) mod p，要求 a, b < p。
func subMod(a, b, p uint64) uint64 {
	if a < b {
		return a + p - b
	}
	return a - b
}

// VulnerableInvert 是待审回调：用分支二进制欧几里得算法求 x 模 p 的逆元，
// 同时返回暴露每轮分支走向的轨迹符号串（仅含 A/B/C/D）。
// 初态 u=x、v=p，直到 u 或 v 等于 1；步数达到 budget 仍未收敛则返回 ErrBudget。
// 轨迹直接泄露秘密相关分支，正是本包要演示的侧信道。
func VulnerableInvert(x, p uint64, budget int) (inv uint64, trace string, err error) {
	if err := ValidateParams(x, p); err != nil {
		return 0, "", err
	}
	u, v := x, p
	// 维护不变式：u ≡ ua*x (mod p)，v ≡ vb*x (mod p)。
	ua, vb := uint64(1), uint64(0)
	var sb strings.Builder
	for u != 1 && v != 1 {
		if sb.Len() >= budget {
			return 0, "", ErrBudget
		}
		switch {
		case u&1 == 0:
			u >>= 1
			ua = halfMod(ua, p)
			sb.WriteByte(StepA)
		case v&1 == 0:
			v >>= 1
			vb = halfMod(vb, p)
			sb.WriteByte(StepB)
		case u > v:
			u = (u - v) >> 1
			ua = halfMod(subMod(ua, vb, p), p)
			sb.WriteByte(StepC)
		default:
			v = (v - u) >> 1
			vb = halfMod(subMod(vb, ua, p), p)
			sb.WriteByte(StepD)
		}
	}
	if u == 1 {
		inv = ua
	} else {
		inv = vb
	}
	return inv, sb.String(), nil
}
