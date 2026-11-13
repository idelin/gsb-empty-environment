package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// UniformSymbol 是加固回调对外轨迹中使用的统一符号。
// 轨迹长度恒为 Rounds(p)，不含任何分支信息。
const UniformSymbol = 'X'

// ErrNotConverged 表示固定轮数结束后状态仍未收敛。
var ErrNotConverged = errors.New("netsec: state did not converge within fixed rounds")

// ErrReferenceMismatch 表示掩码更新结果与大整数参考不一致，回调拒绝输出。
var ErrReferenceMismatch = errors.New("netsec: result disagrees with big.Int reference")

// select64 无分支选择：mask 全 1 时返回 a，全 0 时返回 b。
func select64(mask, a, b uint64) uint64 {
	return b ^ (mask & (a ^ b))
}

// eqMask 在 a==b 时返回全 1，否则全 0（仅用于小整数比较）。
func eqMask(a, b uint64) uint64 {
	d := a ^ b
	// d==0 时 (d|-d)>>63 为 0，减 1 下溢为全 1；否则为 1，减 1 得 0。
	return ((d | (0 - d)) >> 63) - 1
}

// HardenedInvert 是加固回调：执行固定 Rounds(p)=2*bitlen(p)+1 轮，
// 每轮以无分支掩码同时更新四个候选状态 (u, v, ua, vb)，
// 收敛后状态冻结；结束后选择已收敛结果，并与 big.Int 参考核对，
// 不一致或未收敛则拒绝返回。对外轨迹只含固定轮数的统一符号。
//
// 注意：统一符号轨迹与掩码化源码并不证明编译产物在真实机器上指令恒时，
// 生产实现仍需平台级审计（反汇编、时序测量等）。
func HardenedInvert(x, p uint64) (inv uint64, trace string, err error) {
	if err := ValidateParams(x, p); err != nil {
		return 0, "", err
	}
	rounds := Rounds(p)
	u, v := x, p
	ua, vb := uint64(1), uint64(0)
	for i := 0; i < rounds; i++ {
		// 收敛冻结掩码：u==1 或 v==1 时 active=0，状态保持不变。
		conv := eqMask(u, 1) | eqMask(v, 1)
		active := ^conv

		// 分支条件的无分支掩码。
		uEven := (u & 1) - 1 // u 偶 → 全 1
		vEven := (v & 1) - 1 // v 偶 → 全 1
		gt := ((u - v) >> 63) - 1
		mA := uEven
		mB := ^uEven & vEven
		mC := ^uEven & ^vEven & gt
		mD := ^uEven & ^vEven & ^gt

		// 四个候选状态各自四种分支下的新值，全部无分支算出。
		halfU := u >> 1
		halfV := v >> 1
		diffUV := (u - v) >> 1
		diffVU := (v - u) >> 1

		oddUA := 0 - (ua & 1)
		halfUA := (ua + (p & oddUA)) >> 1
		oddVB := 0 - (vb & 1)
		halfVB := (vb + (p & oddVB)) >> 1
		borrowAB := 0 - ((ua - vb) >> 63)
		diffAB := (ua - vb) + (p & borrowAB)
		oddAB := 0 - (diffAB & 1)
		halfDiffAB := (diffAB + (p & oddAB)) >> 1
		borrowBA := 0 - ((vb - ua) >> 63)
		diffBA := (vb - ua) + (p & borrowBA)
		oddBA := 0 - (diffBA & 1)
		halfDiffBA := (diffBA + (p & oddBA)) >> 1

		newU := select64(mA, halfU, select64(mC, diffUV, u))
		newV := select64(mB, halfV, select64(mD, diffVU, v))
		newUA := select64(mA, halfUA, select64(mC, halfDiffAB, ua))
		newVB := select64(mB, halfVB, select64(mD, halfDiffBA, vb))

		u = select64(active, newU, u)
		v = select64(active, newV, v)
		ua = select64(active, newUA, ua)
		vb = select64(active, newVB, vb)
	}

	// 选择已收敛结果。
	converged := (u == 1) || (v == 1)
	if !converged {
		return 0, "", ErrNotConverged
	}
	inv = select64(eqMask(u, 1), ua, vb)

	// 与 big.Int 参考核对，不一致则拒绝。
	ref := new(big.Int).ModInverse(new(big.Int).SetUint64(x), new(big.Int).SetUint64(p))
	if ref == nil {
		return 0, "", fmt.Errorf("netsec: no inverse exists for %d mod %d", x, p)
	}
	if ref.Uint64() != inv {
		return 0, "", fmt.Errorf("%w: masked update gave %d, reference %d", ErrReferenceMismatch, inv, ref.Uint64())
	}
	return inv, strings.Repeat(string(UniformSymbol), rounds), nil
}
