// 精确大整数卷积：多模数 NTT + CRT 恢复符号。
//
// 符号恢复的正确性依据（已证明的系数界）：
//   设 a 长度 n，b 长度 m，Amax = max|a_i|，Bmax = max|b_j|。
//   卷积系数 c_k = sum a_i * b_{k-i}，求和项数至多为 min(n, m)，故
//       |c_k| <= B := min(n, m) * Amax * Bmax   （对所有 k 成立）
//   若模积 M 满足 M > 2B，则区间 (-M/2, M/2] 完整覆盖 [-B, B]，
//   CRT 剩余 x in [0, M) 可按 x > M/2 => x - M 唯一恢复符号。
//   若 M <= 2B，返回错误，绝不静默截断。
package main

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"time"
)

// ---------- 模运算与 NTT ----------

func modPow(base, exp, mod int64) int64 {
	result := int64(1)
	base %= mod
	for exp > 0 {
		if exp&1 == 1 {
			result = result * base % mod
		}
		base = base * base % mod
		exp >>= 1
	}
	return result
}

type nttPrime struct {
	p      int64 // 素数 p = c*2^k + 1
	g      int64 // 原根
	maxLog uint  // k：支持的最大变换长度 2^k
}

// 三个 NTT 素数，模积 M ≈ 7.87e25，足以覆盖测试所用的系数界。
var nttPrimes = []nttPrime{
	{167772161, 3, 25},  // 5 * 2^25 + 1
	{469762049, 3, 26},  // 7 * 2^26 + 1
	{998244353, 3, 23},  // 119 * 2^23 + 1
}

func modulusProduct() *big.Int {
	m := big.NewInt(1)
	for _, pr := range nttPrimes {
		m.Mul(m, big.NewInt(pr.p))
	}
	return m
}

// ntt 原地迭代 NTT；invert 为 true 时做逆变换并乘 n^{-1}。
func ntt(a []int64, invert bool, pr nttPrime) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		// w = g^((p-1)/length) mod p 是 length 次本原单位根
		w := modPow(pr.g, (pr.p-1)/int64(length), pr.p)
		if invert {
			w = modPow(w, pr.p-2, pr.p)
		}
		for i := 0; i < n; i += length {
			wi := int64(1)
			for j := i; j < i+length/2; j++ {
				u := a[j]
				v := a[j+length/2] * wi % pr.p
				a[j] = (u + v) % pr.p
				a[j+length/2] = (u - v + pr.p) % pr.p
				wi = wi * w % pr.p
			}
		}
	}
	if invert {
		invN := modPow(int64(n), pr.p-2, pr.p)
		for i := range a {
			a[i] = a[i] * invN % pr.p
		}
	}
}

// convolveMod 在模 p 下做循环长度足够的线性卷积。
func convolveMod(a, b []int64, pr nttPrime) []int64 {
	need := len(a) + len(b) - 1
	n := 1
	logn := uint(0)
	for n < need {
		n <<= 1
		logn++
	}
	if logn > pr.maxLog {
		panic("序列过长，超出该素数支持的最大 NTT 长度")
	}
	fa := make([]int64, n)
	fb := make([]int64, n)
	for i, v := range a {
		fa[i] = ((v % pr.p) + pr.p) % pr.p
	}
	for i, v := range b {
		fb[i] = ((v % pr.p) + pr.p) % pr.p
	}
	ntt(fa, false, pr)
	ntt(fb, false, pr)
	for i := range fa {
		fa[i] = fa[i] * fb[i] % pr.p
	}
	ntt(fa, true, pr)
	return fa[:need]
}

// ---------- CRT 与符号恢复 ----------

// crtCombine 合并两组剩余：x ≡ r1 (mod m1)，x ≡ r2 (mod m2)，m1、m2 互素。
// 返回新剩余与新模数 m1*m2。
func crtCombine(r1, m1, r2, m2 *big.Int) (*big.Int, *big.Int) {
	// t = (r2 - r1) * m1^{-1} mod m2
	m1Mod := new(big.Int).Mod(m1, m2)
	inv := new(big.Int).ModInverse(m1Mod, m2)
	if inv == nil {
		panic("模数不互素")
	}
	t := new(big.Int).Sub(r2, r1)
	t.Mul(t, inv)
	t.Mod(t, m2)
	x := new(big.Int).Mul(m1, t)
	x.Add(x, r1)
	m := new(big.Int).Mul(m1, m2)
	x.Mod(x, m)
	return x, m
}

// CoeffBound 计算已证明的系数幅值界 B = min(n,m) * Amax * Bmax。
func CoeffBound(a, b []*big.Int) *big.Int {
	maxAbs := func(s []*big.Int) *big.Int {
		m := big.NewInt(0)
		for _, v := range s {
			av := new(big.Int).Abs(v)
			if av.Cmp(m) > 0 {
				m = av
			}
		}
		return m
	}
	n, m := len(a), len(b)
	if m < n {
		n = m
	}
	bound := big.NewInt(int64(n))
	bound.Mul(bound, maxAbs(a))
	bound.Mul(bound, maxAbs(b))
	return bound
}

// ConvolveExact 返回精确大整数卷积系数。
// 若模积 M 不满足 M > 2B（B 为系数界），返回错误而非静默截断。
func ConvolveExact(a, b []*big.Int) ([]*big.Int, *big.Int, error) {
	if len(a) == 0 || len(b) == 0 {
		return nil, nil, errors.New("空序列")
	}
	bound := CoeffBound(a, b)
	M := modulusProduct()
	// 需要 M > 2B，即 M >= 2B+1
	if new(big.Int).Mul(bound, big.NewInt(2)).Cmp(M) >= 0 {
		return nil, bound, fmt.Errorf("模积 M=%s 不足以恢复符号：需要 M > 2B，B=%s", M, bound)
	}

	need := len(a) + len(b) - 1
	// 逐素数取剩余
	residues := make([][]int64, len(nttPrimes))
	for i, pr := range nttPrimes {
		ai := make([]int64, len(a))
		bi := make([]int64, len(b))
		for j, v := range a {
			ai[j] = new(big.Int).Mod(v, big.NewInt(pr.p)).Int64()
		}
		for j, v := range b {
			bi[j] = new(big.Int).Mod(v, big.NewInt(pr.p)).Int64()
		}
		residues[i] = convolveMod(ai, bi, pr)
	}

	out := make([]*big.Int, need)
	half := new(big.Int).Div(M, big.NewInt(2))
	for k := 0; k < need; k++ {
		x := big.NewInt(residues[0][k])
		m := big.NewInt(nttPrimes[0].p)
		for i := 1; i < len(nttPrimes); i++ {
			x, m = crtCombine(x, m, big.NewInt(residues[i][k]), big.NewInt(nttPrimes[i].p))
		}
		// 符号恢复：x in [0, M)，若 x > M/2 则真实值为 x - M
		if x.Cmp(half) > 0 {
			x.Sub(x, M)
		}
		out[k] = x
	}
	return out, bound, nil
}

// ---------- 朴素大整数双循环（基准） ----------

func naiveConvolve(a, b []*big.Int) []*big.Int {
	out := make([]*big.Int, len(a)+len(b)-1)
	for i := range out {
		out[i] = big.NewInt(0)
	}
	for i, av := range a {
		for j, bv := range b {
			term := new(big.Int).Mul(av, bv)
			out[i+j].Add(out[i+j], term)
		}
	}
	return out
}

// ---------- 验证 ----------

func checkEqual(name string, got, want []*big.Int) bool {
	if len(got) != len(want) {
		fmt.Printf("失败[%s]：长度 %d != %d\n", name, len(got), len(want))
		return false
	}
	for i := range got {
		if got[i].Cmp(want[i]) != 0 {
			fmt.Printf("失败[%s]：系数 %d 处 %s != %s\n", name, i, got[i], want[i])
			return false
		}
	}
	return true
}

func randSeq(r *rand.Rand, n int, limit int64) []*big.Int {
	s := make([]*big.Int, n)
	for i := range s {
		s[i] = big.NewInt(r.Int63n(2*limit+1) - limit)
	}
	return s
}

func constSeq(n int, v *big.Int) []*big.Int {
	s := make([]*big.Int, n)
	for i := range s {
		s[i] = new(big.Int).Set(v)
	}
	return s
}

func main() {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	M := modulusProduct()
	fmt.Printf("NTT 素数个数: %d，模积 M = %s（约 %.2e）\n", len(nttPrimes), M, new(big.Float).SetInt(M))

	failures := 0

	// 1) 随机短序列（含负数）
	const trials = 300
	for t := 0; t < trials; t++ {
		n := 1 + r.Intn(12)
		m := 1 + r.Intn(12)
		a := randSeq(r, n, 50)
		b := randSeq(r, m, 50)
		got, bound, err := ConvolveExact(a, b)
		if err != nil {
			fmt.Println("意外错误:", err)
			failures++
			continue
		}
		if t == 0 {
			fmt.Printf("随机测试系数界示例 B = %s（满足 2B < M）\n", bound)
		}
		if !checkEqual("随机", got, naiveConvolve(a, b)) {
			failures++
		}
	}
	fmt.Printf("随机短序列测试：%d 组全部与朴素双循环一致（失败 %d）\n", trials, failures)

	// 2) 系数达到界 B 的边界测试：全幅值序列使中间系数恰为 ±B
	for _, sz := range []int{2, 5, 9} {
		Amax := big.NewInt(1000003)
		Bmax := big.NewInt(999983)
		a := constSeq(sz, Amax)
		b := constSeq(sz, Bmax)
		got, bound, err := ConvolveExact(a, b)
		if err != nil {
			fmt.Println("意外错误:", err)
			failures++
			continue
		}
		if !checkEqual("正边界", got, naiveConvolve(a, b)) {
			failures++
		}
		// 全负：中间系数为 -B，检验符号恢复
		negA := constSeq(sz, new(big.Int).Neg(Amax))
		gotNeg, _, err := ConvolveExact(negA, b)
		if err != nil {
			fmt.Println("意外错误:", err)
			failures++
			continue
		}
		if !checkEqual("负边界", gotNeg, naiveConvolve(negA, b)) {
			failures++
		}
		fmt.Printf("边界测试 n=m=%d：系数界 B = %s，中间系数 = %s（= ±B，已验证）\n",
			sz, bound, got[sz-1])
	}

	// 3) 系数逼近模积符号恢复边界 ±M/2：令 2B 恰好略小于 M
	{
		n := 4
		// B = n * Amax * 1，取 Amax = (M-1)/(2n) 使 2B <= M-1 < M
		Amax := new(big.Int).Sub(M, big.NewInt(1))
		Amax.Div(Amax, big.NewInt(int64(2*n)))
		a := constSeq(n, Amax)
		b := constSeq(n, big.NewInt(1))
		bound := CoeffBound(a, b)
		twoB := new(big.Int).Mul(bound, big.NewInt(2))
		fmt.Printf("近模积边界：B = %s，M - 2B = %s（>0，可恢复）\n", bound, new(big.Int).Sub(M, twoB))
		got, _, err := ConvolveExact(a, b)
		if err != nil {
			fmt.Println("意外错误:", err)
			failures++
		} else if !checkEqual("近模积正边界", got, naiveConvolve(a, b)) {
			failures++
		}
		negA := constSeq(n, new(big.Int).Neg(Amax))
		gotNeg, _, err := ConvolveExact(negA, b)
		if err != nil {
			fmt.Println("意外错误:", err)
			failures++
		} else if !checkEqual("近模积负边界", gotNeg, naiveConvolve(negA, b)) {
			failures++
		}
	}

	// 4) 拒绝路径：2B >= M 时必须报错，不允许静默截断
	{
		n := 4
		Amax := new(big.Int).Div(M, big.NewInt(int64(2*n))) // 使 2B >= M
		Amax.Add(Amax, big.NewInt(1))
		a := constSeq(n, Amax)
		b := constSeq(n, big.NewInt(1))
		_, bound, err := ConvolveExact(a, b)
		if err == nil {
			fmt.Println("失败：2B >= M 时未报错（存在静默截断风险）")
			failures++
		} else {
			fmt.Printf("拒绝路径正常：B = %s 时返回错误：%v\n", bound, err)
		}
	}

	if failures == 0 {
		fmt.Println("全部测试通过：模变换结果与朴素大整数双循环完全一致，无静默截断。")
	} else {
		fmt.Printf("共 %d 处失败！\n", failures)
	}
}
