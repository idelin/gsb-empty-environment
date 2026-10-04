// robustline: 鲁棒直线拟合器。
//
// 给定 n 个二维整数点 (n <= 20)，剔除至多 k 个离群点，
// 求一条直线使其余点到直线的最大垂直距离最小。
//
// 直线统一用一般式 a*x + b*y + c = 0 表示（法向量 (a,b)），
// 因此竖直线 (b=0) 与水平线 (a=0) 被自然覆盖，无斜率奇点。
// 全部比较在大整数上进行，结果精确。
//
// 算法要点：
//   - 固定法向 n 时，点投影 t_i = n·p_i 为整数；保留 m=n-k 个点
//     的最优是投影值排序后长度为 m 的最小窗口，误差 = Δt/(2|n|)。
//   - 最优直线的方向必平行于某两个输入点的连线（临界方向），
//     故枚举所有点对的法向即可得到全局最优。
//
// 输入（stdin）：
//
//	第一行: n k
//	接下来 n 行: x_i y_i（整数）
//
// 输出：最优直线、最优误差（精确形式+小数）、被剔除点、
//
//	保留点残差及达到最大误差的证据点。
package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"
	"sort"
)

type point struct {
	x, y *big.Int
	id   int // 1-based 输入编号
}

// candidate 记录一个候选最优解。
type candidate struct {
	a, b *big.Int // 法向 n=(a,b)，直线为 a*x+b*y+c=0
	c    *big.Int
	dt   *big.Int // 保留窗口投影跨度 t_hi - t_lo
	norm *big.Int // |n|^2 = a^2+b^2
	tLo  *big.Int // 窗口投影下界（用于判定剔除点）
	tHi  *big.Int // 窗口投影上界
}

// err2Num/err2Den 给出误差平方的分子分母（未约分）：
// err = dt/(2*|n|)  =>  err^2 = dt^2 / (4*norm)
func (c *candidate) err2Num() *big.Int { return new(big.Int).Mul(c.dt, c.dt) }
func (c *candidate) err2Den() *big.Int {
	return new(big.Int).Mul(big.NewInt(4), c.norm)
}

// lessErr 精确比较两个候选的误差：dt1^2/norm1 < dt2^2/norm2 ?
func lessErr(c1, c2 *candidate) bool {
	l := new(big.Int).Mul(c1.err2Num(), c2.norm)
	r := new(big.Int).Mul(c2.err2Num(), c1.norm)
	return l.Cmp(r) < 0
}

func proj(a, b *big.Int, p point) *big.Int {
	t := new(big.Int).Mul(a, p.x)
	return t.Add(t, new(big.Int).Mul(b, p.y))
}

func main() {
	in := bufio.NewReader(os.Stdin)
	var n, k int
	if _, err := fmt.Fscan(in, &n, &k); err != nil {
		fmt.Fprintln(os.Stderr, "用法: 第一行 'n k'，随后 n 行整数坐标 'x y'")
		os.Exit(1)
	}
	if n < 1 || n > 20 || k < 0 || k >= n {
		fmt.Fprintln(os.Stderr, "需要 1 <= n <= 20 且 0 <= k < n")
		os.Exit(1)
	}
	pts := make([]point, n)
	for i := range pts {
		var x, y big.Int
		if _, err := fmt.Fscan(in, &x, &y); err != nil {
			fmt.Fprintf(os.Stderr, "读取第 %d 个点失败\n", i+1)
			os.Exit(1)
		}
		pts[i] = point{x: &x, y: &y, id: i + 1}
	}
	m := n - k // 至少保留的点数

	var best *candidate

	// 枚举所有点对的法向（临界方向）。方向取 d=pj-pi，法向 n=(-dy,dx)。
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dx := new(big.Int).Sub(pts[j].x, pts[i].x)
			dy := new(big.Int).Sub(pts[j].y, pts[i].y)
			if dx.Sign() == 0 && dy.Sign() == 0 {
				continue // 重合点，无方向
			}
			a := new(big.Int).Neg(dy)
			b := new(big.Int).Set(dx)

			// 投影并排序
			type tp struct {
				t   *big.Int
				idx int
			}
			ts := make([]tp, n)
			for l, p := range pts {
				ts[l] = tp{proj(a, b, p), l}
			}
			sort.Slice(ts, func(p, q int) bool { return ts[p].t.Cmp(ts[q].t) < 0 })

			norm := new(big.Int).Add(new(big.Int).Mul(a, a), new(big.Int).Mul(b, b))

			// 长度为 m 的最小投影窗口
			for lo := 0; lo+m <= n; lo++ {
				hi := lo + m - 1
				dt := new(big.Int).Sub(ts[hi].t, ts[lo].t)
				// 中线: a*x+b*y - (t_lo+t_hi)/2 = 0
				// 整数化: 2a*x + 2b*y - (t_lo+t_hi) = 0
				cand := &candidate{
					a:    new(big.Int).Mul(a, big.NewInt(2)),
					b:    new(big.Int).Mul(b, big.NewInt(2)),
					c:    new(big.Int).Neg(new(big.Int).Add(ts[lo].t, ts[hi].t)),
					dt:   dt,
					norm: new(big.Int).Set(norm),
					tLo:  ts[lo].t,
					tHi:  ts[hi].t,
				}
				if best == nil || lessErr(cand, best) {
					best = cand
				}
			}
		}
	}

	if best == nil {
		// 所有点重合：任取过该点的直线，误差 0
		best = &candidate{
			a:    big.NewInt(1),
			b:    big.NewInt(0),
			c:    new(big.Int).Neg(pts[0].x),
			dt:   big.NewInt(0),
			norm: big.NewInt(1),
			tLo:  new(big.Int).Set(pts[0].x),
			tHi:  new(big.Int).Set(pts[0].x),
		}
	}

	normalize(best)
	report(pts, best, k)
}

// normalize 将 (a,b,c) 除以最大公约数并固定符号，得到规范一般式。
func normalize(c *candidate) {
	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(c.a), new(big.Int).Abs(c.b))
	g.GCD(nil, nil, g, new(big.Int).Abs(c.c))
	if g.Sign() > 0 {
		c.a.Div(c.a, g)
		c.b.Div(c.b, g)
		c.c.Div(c.c, g)
	}
	if c.a.Sign() < 0 || (c.a.Sign() == 0 && c.b.Sign() < 0) {
		c.a.Neg(c.a)
		c.b.Neg(c.b)
		c.c.Neg(c.c)
	}
}

// residual2 返回点 p 到直线误差平方的分子、分母：
// r^2 = (a*x+b*y+c)^2 / (a^2+b^2)
func residual2(c *candidate, p point) (num, den *big.Int) {
	v := proj(c.a, c.b, p)
	v.Add(v, c.c)
	num = new(big.Int).Mul(v, v)
	den = new(big.Int).Add(new(big.Int).Mul(c.a, c.a), new(big.Int).Mul(c.b, c.b))
	return
}

func ratFloat(num, den *big.Int) *big.Float {
	f := new(big.Float).SetPrec(128).SetInt(num)
	return f.Quo(f, new(big.Float).SetInt(den))
}

// sqrtFloat 用牛顿法求 sqrt(num/den) 的近似值。
func sqrtFloat(num, den *big.Int) *big.Float {
	x := ratFloat(num, den)
	if x.Sign() == 0 {
		return x
	}
	g := new(big.Float).SetPrec(256).Copy(x)
	two := new(big.Float).SetPrec(256).SetInt64(2)
	tmp := new(big.Float).SetPrec(256)
	for i := 0; i < 200; i++ {
		tmp.Quo(x, g)
		g.Add(g, tmp)
		g.Quo(g, two)
	}
	return g
}

func report(pts []point, best *candidate, k int) {
	type res struct {
		p        point
		num, den *big.Int // 残差平方
	}
	kept := []res{}
	excluded := []point{}

	// 判定剔除：残差大于最优误差者。最优误差平方 = dt^2/(4*norm)，
	// 全部用整数交叉相乘精确比较。
	bestNum := new(big.Int).Mul(best.dt, best.dt)
	bestDen := new(big.Int).Mul(big.NewInt(4), best.norm)

	for _, p := range pts {
		num, den := residual2(best, p)
		// num/den > bestNum/bestDen ?
		l := new(big.Int).Mul(num, bestDen)
		r := new(big.Int).Mul(bestNum, den)
		if l.Cmp(r) > 0 {
			excluded = append(excluded, p)
		} else {
			kept = append(kept, res{p, num, den})
		}
	}

	// 保留点的最大残差（精确）
	var maxNum, maxDen *big.Int
	maxIdx := []int{}
	for i, kp := range kept {
		if maxNum == nil {
			maxNum, maxDen = kp.num, kp.den
			maxIdx = []int{i}
			continue
		}
		l := new(big.Int).Mul(kp.num, maxDen)
		r := new(big.Int).Mul(maxNum, kp.den)
		switch l.Cmp(r) {
		case 1:
			maxNum, maxDen = kp.num, kp.den
			maxIdx = []int{i}
		case 0:
			maxIdx = append(maxIdx, i)
		}
	}
	// 约分最大残差平方
	g := new(big.Int).GCD(nil, nil, maxNum, maxDen)
	redNum := new(big.Int).Div(new(big.Int).Set(maxNum), g)
	redDen := new(big.Int).Div(new(big.Int).Set(maxDen), g)

	fmt.Println("=== 最优直线（一般式）===")
	fmt.Printf("%s*x + %s*y + %s = 0\n", best.a, best.b, best.c)
	fmt.Println()
	fmt.Println("=== 最优误差 ===")
	fmt.Printf("max 垂直残差 = sqrt(%s/%s)", redNum, redDen)
	if redDen.Cmp(big.NewInt(1)) == 0 {
		fmt.Printf(" = sqrt(%s)", redNum)
	}
	fmt.Printf(" ≈ %s\n", sqrtFloat(redNum, redDen).Text('g', 12))
	fmt.Println()
	fmt.Printf("=== 剔除点（%d 个，上限 %d）===\n", len(excluded), k)
	for _, p := range excluded {
		fmt.Printf("  #%d (%s, %s)\n", p.id, p.x, p.y)
	}
	fmt.Println()
	fmt.Printf("=== 保留点（%d 个）及残差 ===\n", len(kept))
	maxSet := map[int]bool{}
	for _, i := range maxIdx {
		maxSet[i] = true
	}
	for i, kp := range kept {
		mark := ""
		if maxSet[i] {
			mark = "  <-- 达到最大误差（证据点）"
		}
		fmt.Printf("  #%d (%s, %s): r = %s%s\n",
			kp.p.id, kp.p.x, kp.p.y,
			sqrtFloat(kp.num, kp.den).Text('g', 12), mark)
	}
	fmt.Println()
	fmt.Println("=== 最优性证据 ===")
	fmt.Printf("误差由以下 %d 个证据点同时达到，分布于直线两侧（或线上）：\n", len(maxIdx))
	for _, i := range maxIdx {
		p := kept[i].p
		v := proj(best.a, best.b, p)
		v.Add(v, best.c)
		side := "线上"
		if v.Sign() > 0 {
			side = "正侧"
		} else if v.Sign() < 0 {
			side = "负侧"
		}
		fmt.Printf("  #%d (%s, %s)  [%s]\n", p.id, p.x, p.y, side)
	}
	fmt.Println("已枚举全部 C(n,2) 个临界方向（直线平行于两点连线）,")
	fmt.Println("每个方向取投影最小 m 窗口，比较全程使用大整数精确运算，")
	fmt.Println("故上述误差为全局最优（含竖直线情形）。")
}
