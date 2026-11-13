package netsec

import "math/big"

// 确定性 Miller-Rabin 基集合：对所有 n < 2^64 的整数，按该顺序做七轮
// Miller-Rabin 检验的结论是确定性正确的（无伪素数通过）。
//
// 该基集合的充分性由 Jaeschke (1993) 与 Jiang/Deng (2014) 针对 32 位与
// 64 位范围分别验证。
var deterministicBases64 = []uint64{2, 3, 5, 7, 11, 13, 17}

// Primality 表示一个整数的素性结构结论。
type Primality int

const (
	// PrimalityUnknown 表示当前参数范围与回调不足以给出确定性结论。
	PrimalityUnknown Primality = iota
	// PrimalityPrime 表示由确定性检验或宿主回调确认为素数。
	PrimalityPrime
	// PrimalityComposite 表示由确定性检验或宿主回调确认为合数。
	PrimalityComposite
)

// String 返回素性结论的可读描述。
func (p Primality) String() string {
	switch p {
	case PrimalityPrime:
		return "prime"
	case PrimalityComposite:
		return "composite"
	default:
		return "unknown"
	}
}

// millerRabin 对 n（奇数，n > 2）执行一次以 a 为基的 Miller-Rabin 强伪素数检验。
// 返回 true 表示 n 通过该基检验（可能是素数），false 表示 n 一定是合数。
func millerRabin(n, a *big.Int) bool {
	// d 与 r 满足 n-1 = d * 2^r，d 为奇数。
	d := new(big.Int).Sub(n, big.NewInt(1))
	r := 0
	for d.Bit(0) == 0 {
		d.Rsh(d, 1)
		r++
	}

	nMinusOne := new(big.Int).Sub(n, big.NewInt(1))
	x := new(big.Int).Exp(a, d, n)

	if x.Cmp(big.NewInt(1)) == 0 || x.Cmp(nMinusOne) == 0 {
		return true
	}
	for i := 0; i < r-1; i++ {
		x.Mul(x, x)
		x.Mod(x, n)
		if x.Cmp(nMinusOne) == 0 {
			return true
		}
	}
	return false
}

// IsPrime64 对 64 位无符号整数执行确定性 Miller-Rabin 素性检验。
// n < 2^64 时结论确定；n 达到或超过 2^64 时返回 false 与 ok=false，
// 调用方必须改用宿主注入的素性回调，不能把该 false 当作合数结论。
func IsPrime64(n *big.Int) (prime bool, ok bool) {
	if n == nil || n.Sign() < 0 {
		return false, false
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 64)
	if n.Cmp(limit) >= 0 {
		return false, false
	}

	switch n.Uint64() {
	case 0, 1:
		return false, true
	case 2, 3:
		return true, true
	}
	if n.Bit(0) == 0 {
		return false, true
	}
	for _, base := range deterministicBases64 {
		a := new(big.Int).SetUint64(base)
		if a.Cmp(n) >= 0 {
			continue
		}
		if !millerRabin(n, a) {
			return false, true
		}
	}
	return true, true
}

// PrimalityCallback 是宿主注入的素性判定回调。返回 true 表示宿主确认 n 为素数，
// false 表示宿主确认 n 为合数。包内对 64 位以内的整数始终优先使用确定性
// Miller-Rabin 基集合，仅在超出 64 位时才调用回调；回调结论会如实标注来源，
// 包本身不会在缺乏依据时把任何整数称为素数。
type PrimalityCallback func(n *big.Int) bool

// classifyPrimality 在可用证据范围内给出 n 的素性结论。
// 64 位以内使用确定性基集合；超出范围时仅在提供回调的情况下给出结论，
// 否则返回 PrimalityUnknown，杜绝把合数误称为素数。
func classifyPrimality(n *big.Int, cb PrimalityCallback) Primality {
	if prime, ok := IsPrime64(n); ok {
		if prime {
			return PrimalityPrime
		}
		return PrimalityComposite
	}
	if cb != nil {
		if cb(new(big.Int).Set(n)) {
			return PrimalityPrime
		}
		return PrimalityComposite
	}
	return PrimalityUnknown
}
