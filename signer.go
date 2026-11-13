package netsec

import (
	"errors"
	"math/big"
)

// Branch 标识发生故障的 CRT 分支。
type Branch int

const (
	// BranchP 表示模 p 分支（s_p = m^dp mod p）。
	BranchP Branch = iota
	// BranchQ 表示模 q 分支（s_q = m^dq mod q）。
	BranchQ
)

// String 返回分支名。
func (b Branch) String() string {
	switch b {
	case BranchP:
		return "p-branch"
	case BranchQ:
		return "q-branch"
	default:
		return "unknown-branch"
	}
}

// ErrFaultPosition 在调用方要求翻转的位超出分支模数位宽时返回。
// 这类故障无法在“分支余数仍落在 [0,modulus) 内”的约束下注入，
// 调用方应保留该尝试作为不能分解的证据，而不是悄悄截断。
var ErrFaultPosition = errors.New("netsec: fault bit position is outside the branch modulus")

// CRTSigner 是仅用于本地实验的小型 RSA CRT 签名器。
type CRTSigner struct {
	n    *big.Int
	e    *big.Int
	p    *big.Int
	q    *big.Int
	dp   *big.Int
	dq   *big.Int
	qInv *big.Int
}

// NewCRTSigner 用素因子 p、q 与公开指数 e 构造 CRT 签名器。
// p、q 必须是可由确定性 64 位检验确认的不同奇素数，且 e 与 (p-1)(q-1) 互素。
func NewCRTSigner(p, q, e *big.Int) (*CRTSigner, error) {
	if p == nil || q == nil || e == nil {
		return nil, errors.New("netsec: p, q and e must be non-nil")
	}
	pPrime, pOK := IsPrime64(p)
	qPrime, qOK := IsPrime64(q)
	if !pOK || !qOK {
		return nil, errors.New("netsec: experimental signer requires deterministic 64-bit primes")
	}
	if !pPrime || !qPrime {
		return nil, errors.New("netsec: p and q must both be prime")
	}
	if p.Cmp(q) == 0 {
		return nil, errors.New("netsec: p and q must be distinct")
	}

	n := new(big.Int).Mul(p, q)
	if e.Cmp(big.NewInt(1)) <= 0 || e.Cmp(n) >= 0 {
		return nil, errors.New("netsec: require 1 < e < N")
	}

	phi := new(big.Int).Mul(new(big.Int).Sub(p, one), new(big.Int).Sub(q, one))
	if g := new(big.Int).GCD(nil, nil, e, phi); g.Cmp(one) != 0 {
		return nil, errors.New("netsec: e must be coprime to (p-1)(q-1)")
	}

	d := new(big.Int).ModInverse(e, phi)
	dp := new(big.Int).Mod(d, new(big.Int).Sub(p, one))
	dq := new(big.Int).Mod(d, new(big.Int).Sub(q, one))
	qInv := new(big.Int).ModInverse(q, p)

	return &CRTSigner{
		n:    n,
		e:    new(big.Int).Set(e),
		p:    new(big.Int).Set(p),
		q:    new(big.Int).Set(q),
		dp:   dp,
		dq:   dq,
		qInv: qInv,
	}, nil
}

// N 返回模数副本。
func (c *CRTSigner) N() *big.Int { return new(big.Int).Set(c.n) }

// P 返回素因子 p 的副本。
func (c *CRTSigner) P() *big.Int { return new(big.Int).Set(c.p) }

// Q 返回素因子 q 的副本。
func (c *CRTSigner) Q() *big.Int { return new(big.Int).Set(c.q) }

// Sign 返回 m 的正常 CRT 签名。要求 0 <= m < N。
func (c *CRTSigner) Sign(m *big.Int) (*big.Int, error) {
	if m == nil || m.Sign() < 0 || m.Cmp(c.n) >= 0 {
		return nil, errors.New("netsec: m must be non-negative and smaller than N")
	}
	sp := new(big.Int).Exp(m, c.dp, c.p)
	sq := new(big.Int).Exp(m, c.dq, c.q)
	return c.combine(sp, sq), nil
}

// BranchResults 返回两个分支未注入故障的模幂余数，便于测试对照。
func (c *CRTSigner) BranchResults(m *big.Int) (sp, sq *big.Int, err error) {
	if m == nil || m.Sign() < 0 || m.Cmp(c.n) >= 0 {
		return nil, nil, errors.New("netsec: m must be non-negative and smaller than N")
	}
	return new(big.Int).Exp(m, c.dp, c.p), new(big.Int).Exp(m, c.dq, c.q), nil
}

// SignWithBitFault 在指定分支的模幂结果上翻转 bit 位后再重组签名。
// 翻转后的余数必须仍在 [0, modulus) 内；bit 超出该模数位宽时返回
// ErrFaultPosition，调用方可据此保留“无法注入/无法分解”的证据。
func (c *CRTSigner) SignWithBitFault(m *big.Int, branch Branch, bit uint) (*big.Int, error) {
	if m == nil || m.Sign() < 0 || m.Cmp(c.n) >= 0 {
		return nil, errors.New("netsec: m must be non-negative and smaller than N")
	}

	sp := new(big.Int).Exp(m, c.dp, c.p)
	sq := new(big.Int).Exp(m, c.dq, c.q)

	switch branch {
	case BranchP:
		faulted, err := flipWithin(sp, c.p, bit)
		if err != nil {
			return nil, err
		}
		return c.combine(faulted, sq), nil
	case BranchQ:
		faulted, err := flipWithin(sq, c.q, bit)
		if err != nil {
			return nil, err
		}
		return c.combine(sp, faulted), nil
	default:
		return nil, errors.New("netsec: unknown CRT branch")
	}
}

// flipWithin 翻转 value 的第 bit 位，并确认结果仍严格小于 modulus。
func flipWithin(value, modulus *big.Int, bit uint) (*big.Int, error) {
	mask := new(big.Int).Lsh(one, bit)
	if mask.Cmp(modulus) >= 0 {
		return nil, ErrFaultPosition
	}
	faulted := new(big.Int).Xor(value, mask)
	if faulted.Cmp(modulus) >= 0 {
		return nil, ErrFaultPosition
	}
	return faulted, nil
}

// combine 用 Garner 公式重组 CRT 签名：
// s = sq + q * (qInv * (sp - sq) mod p)。
func (c *CRTSigner) combine(sp, sq *big.Int) *big.Int {
	diff := new(big.Int).Sub(sp, sq)
	diff.Mod(diff, c.p)
	h := new(big.Int).Mul(c.qInv, diff)
	h.Mod(h, c.p)

	s := new(big.Int).Mul(c.q, h)
	s.Add(s, sq)
	return s
}
