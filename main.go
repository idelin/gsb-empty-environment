// robustline: 鲁棒直线拟合器
//
// 给定平面上至多 20 个整数坐标点，剔除至多 k 个离群点，拟合一条直线
// （一般式 ax+by+c=0），使保留点到直线的最大垂直距离最小。竖直线同样支持。
//
// 算法：
//   对固定点集，"最小化最大垂直残差" 的最优直线是点集最窄包含条带的中线，
//   最优误差 = 条带宽度 / 2。条带宽度关于法向方向是分段正弦函数，每段内部
//   唯一的驻点是极大值，故最小值必在分段点处取得；分段点即某两点连线方向
//   的法向（"事件方向"）。因此枚举全部 O(n^2) 个点对确定的法向即可，竖直线
//   （法向水平）自然被覆盖，无需按斜率表示。
//   对每个法向，把所有点投影到法向上（整数），排序后取覆盖至少 n-k 个点的
//   最窄窗口；窗口外的点至多 k 个，即为被剔除的离群点。
//   不同方向的误差比较用整数交叉相乘完成（math/big 兜底），完全精确。
//
// 输入（stdin 或命令行指定的文件）：
//   n k
//   x1 y1
//   ...
//   xn yn
//
// 输出：最优直线一般式、被剔除点、精确最大垂直残差及最优性证据点。
//
// 坐标范围假设：|x|,|y| <= 10^6（投影与宽度比较均在 int64 内安全）。
package main

import (
	"bufio"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
)

type point struct {
	x, y int64
	id   int // 1-based 编号
}

func gcd64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

type solution struct {
	found  bool
	nx, ny int64 // 条带法向（直线法向）
	normSq int64 // nx^2+ny^2
	extent int64 // 窗口投影宽度
	lo, hi int64 // 窗口投影区间 [lo, hi]
}

// better 报告候选 (extent=e, normSq=ns) 是否严格优于当前最优。
// 误差^2 = e^2 / (4*ns)，交叉相乘精确比较，避免浮点误差。
func (s *solution) better(e, ns int64) bool {
	if !s.found {
		return true
	}
	l := new(big.Int).Mul(big.NewInt(e), big.NewInt(e))
	l.Mul(l, big.NewInt(s.normSq))
	r := new(big.Int).Mul(big.NewInt(s.extent), big.NewInt(s.extent))
	r.Mul(r, big.NewInt(ns))
	return l.Cmp(r) < 0
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

// lineString 把一般式系数格式化为 "ax + by + c"（省略零系数的符号噪音）。
func lineString(a, b, c int64) string {
	s := ""
	term := func(coef int64, name string) {
		if coef == 0 {
			return
		}
		v := coef
		sign := ""
		if s != "" {
			sign = " + "
			if v < 0 {
				sign = " - "
				v = -v
			}
		}
		s += fmt.Sprintf("%s%d%s", sign, v, name)
	}
	term(a, "x")
	term(b, "y")
	term(c, "")
	return s
}

func main() {
	in := os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		in = f
	}
	r := bufio.NewReader(in)
	var n, k int
	if _, err := fmt.Fscan(r, &n, &k); err != nil {
		fatal(fmt.Errorf("读取 n k 失败: %v", err))
	}
	if n < 1 || n > 20 {
		fatal(fmt.Errorf("点数 n 须满足 1 <= n <= 20，实际 %d", n))
	}
	if k < 0 || k >= n {
		fatal(fmt.Errorf("k 须满足 0 <= k < n，实际 n=%d k=%d", n, k))
	}
	pts := make([]point, n)
	for i := range pts {
		if _, err := fmt.Fscan(r, &pts[i].x, &pts[i].y); err != nil {
			fatal(fmt.Errorf("读取第 %d 个点失败: %v", i+1, err))
		}
		pts[i].id = i + 1
	}
	m := n - k // 至少保留的点数

	best, numDirs := solve(pts, m)

	// 直线一般式：2*nx*x + 2*ny*y - (lo+hi) = 0，约去公因子并定向。
	a, b, c := 2*best.nx, 2*best.ny, -(best.lo + best.hi)
	if g := gcd64(gcd64(a, b), c); g > 1 {
		a, b, c = a/g, b/g, c/g
	}
	if a < 0 || (a == 0 && b < 0) {
		a, b, c = -a, -b, -c
	}

	// 分类：严格在窗口外的点必须剔除（至多 k 个）；边界上的点保留。
	var excluded, witnessLo, witnessHi []point
	kept := 0
	for _, p := range pts {
		proj := best.nx*p.x + best.ny*p.y
		switch {
		case proj < best.lo || proj > best.hi:
			excluded = append(excluded, p)
		default:
			kept++
			if proj == best.lo {
				witnessLo = append(witnessLo, p)
			}
			if proj == best.hi && best.extent > 0 {
				witnessHi = append(witnessHi, p)
			}
		}
	}
	if len(excluded) > k {
		fatal(fmt.Errorf("内部错误：剔除 %d 个点超过上限 %d", len(excluded), k))
	}

	// 精确误差：残差 = |a x + b y + c| / sqrt(a^2+b^2)，最大值在边界取得。
	// 残差^2 = extent^2 / (4 * normSq)，约分输出。
	num := best.extent * best.extent
	den := 4 * best.normSq
	if g := gcd64(num, den); g > 1 {
		num, den = num/g, den/g
	}
	resid := math.Sqrt(float64(best.extent*best.extent)) / (2 * math.Sqrt(float64(best.normSq)))

	fmt.Printf("最优直线（一般式）: %s = 0\n", lineString(a, b, c))
	fmt.Printf("保留点数: %d，剔除点数: %d（允许至多 %d）\n", kept, len(excluded), k)
	if len(excluded) > 0 {
		fmt.Println("被剔除的离群点:")
		for _, p := range excluded {
			fmt.Printf("  #%d (%d, %d)\n", p.id, p.x, p.y)
		}
	}
	fmt.Printf("最大垂直残差: %.10g\n", resid)
	fmt.Printf("  精确值: sqrt(%d/%d)，即残差^2 = %d/%d\n", num, den, num, den)
	fmt.Println("最优性证据:")
	fmt.Printf("  枚举了全部 %d 个候选法向（由点对确定），本解误差严格不劣于任一候选。\n", numDirs)
	if best.extent == 0 {
		fmt.Println("  残差为 0：保留点共线，显然最优。")
	} else {
		fmt.Printf("  下边界 %s = 0 上的点:", lineString(2*best.nx, 2*best.ny, -2*best.lo))
		for _, p := range witnessLo {
			fmt.Printf(" #%d(%d,%d)", p.id, p.x, p.y)
		}
		fmt.Println()
		fmt.Printf("  上边界 %s = 0 上的点:", lineString(2*best.nx, 2*best.ny, -2*best.hi))
		for _, p := range witnessHi {
			fmt.Printf(" #%d(%d,%d)", p.id, p.x, p.y)
		}
		fmt.Println()
		fmt.Println("  两侧边界平行且均压有保留点，条带无法再收窄，故残差不可再降。")
	}
}

// solve 在 pts 中选出至少 m 个点，使最窄包含条带宽度最小。
// 返回最优解及候选法向数量。
func solve(pts []point, m int) (solution, int) {
	n := len(pts)
	// 枚举候选法向：所有点对差向量的法向，归一化（约分+定向）去重。
	type dir struct{ nx, ny int64 }
	dirs := make(map[dir]bool)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dx := pts[j].x - pts[i].x
			dy := pts[j].y - pts[i].y
			if dx == 0 && dy == 0 {
				continue
			}
			g := gcd64(dx, dy)
			nx, ny := -dy/g, dx/g
			if nx < 0 || (nx == 0 && ny < 0) {
				nx, ny = -nx, -ny
			}
			dirs[dir{nx, ny}] = true
		}
	}

	var best solution
	if len(dirs) == 0 {
		// 所有点重合：任取过该点的直线，误差 0。
		best = solution{found: true, nx: 1, ny: 0, normSq: 1, extent: 0, lo: pts[0].x, hi: pts[0].x}
	}
	for d := range dirs {
		ns := d.nx*d.nx + d.ny*d.ny
		projs := make([]int64, n)
		order := make([]int, n)
		for i := range pts {
			projs[i] = d.nx*pts[i].x + d.ny*pts[i].y
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return projs[order[a]] < projs[order[b]] })
		// 覆盖至少 m 个点的最窄窗口必以某两个投影值为端点。
		for i := 0; i+m-1 < n; i++ {
			lo := projs[order[i]]
			hi := projs[order[i+m-1]]
			if best.better(hi-lo, ns) {
				best = solution{found: true, nx: d.nx, ny: d.ny, normSq: ns, extent: hi - lo, lo: lo, hi: hi}
			}
		}
	}
	return best, len(dirs)
}
