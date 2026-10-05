package exactmatrix

import (
	"math/big"
	"math/rand"
	"testing"
)

func ratI(v int64) big.Rat { return *big.NewRat(v, 1) }

func ratsFromInts(vs ...int64) []big.Rat {
	r := make([]big.Rat, len(vs))
	for i, v := range vs {
		r[i].SetInt64(v)
	}
	return r
}

func rowsFromInts(vals ...[]int64) [][]big.Rat {
	rows := make([][]big.Rat, len(vals))
	for i, vs := range vals {
		rows[i] = ratsFromInts(vs...)
	}
	return rows
}

// matVec 计算 A·x（A 为行切片）。
func matVec(a [][]big.Rat, x []big.Rat) []big.Rat {
	n := len(a)
	out := make([]big.Rat, n)
	for i := 0; i < n; i++ {
		acc := new(big.Rat)
		for j := range x {
			acc.Add(acc, new(big.Rat).Mul(&a[i][j], &x[j]))
		}
		out[i].Set(acc)
	}
	return out
}

func vecEq(a, b []big.Rat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(&b[i]) != 0 {
			return false
		}
	}
	return true
}

func vecIsZero(a []big.Rat) bool {
	for i := range a {
		if a[i].Sign() != 0 {
			return false
		}
	}
	return true
}

// snapshot 从 Matrix 导出当前矩阵的深拷贝，用于独立核对。
func snapshot(m *Matrix) [][]big.Rat {
	n := m.N()
	a := make([][]big.Rat, n)
	for i := range a {
		a[i] = m.Row(i)
	}
	return a
}

// checkConsistent 用全量精确消元独立核对 Matrix 的缓存状态。
func checkConsistent(t *testing.T, m *Matrix) {
	t.Helper()
	a := snapshot(m)
	wantDet := detElim(a)
	if got := m.Det(); got.Cmp(&wantDet) != 0 {
		t.Fatalf("行列式不一致: 增量维护得 %v, 全量消元得 %v", &got, &wantDet)
	}
	if inv := m.Inverse(); inv != nil {
		n := m.N()
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				acc := new(big.Rat)
				for k := 0; k < n; k++ {
					acc.Add(acc, new(big.Rat).Mul(&a[i][k], &inv[k][j]))
				}
				want := int64(0)
				if i == j {
					want = 1
				}
				if acc.Cmp(big.NewRat(want, 1)) != 0 {
					t.Fatalf("A·A⁻¹ ≠ I: (%d,%d) = %v", i, j, acc)
				}
			}
		}
	} else if wantDet.Sign() != 0 {
		t.Fatalf("缓存称奇异，但全量消元行列式为 %v", &wantDet)
	}
}

// TestRandomShortUpdates 随机短更新：每步 SetRow 后与全量精确消元核对，
// 并在可逆时验证 Solve 的解可精确代回。
func TestRandomShortUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		n := 2 + rng.Intn(5)
		rows := make([][]big.Rat, n)
		for i := range rows {
			rows[i] = make([]big.Rat, n)
			for j := range rows[i] {
				rows[i][j].SetInt64(int64(rng.Intn(11) - 5))
			}
		}
		m, err := NewFromRows(rows)
		if err != nil {
			t.Fatal(err)
		}
		checkConsistent(t, m)
		steps := 8 + rng.Intn(12)
		for s := 0; s < steps; s++ {
			i := rng.Intn(n)
			nr := make([]big.Rat, n)
			for j := range nr {
				// 混入分数项，确保不是纯整数路径。
				nr[j].SetFrac64(int64(rng.Intn(11)-5), int64(1+rng.Intn(4)))
			}
			if err := m.SetRow(i, nr); err != nil {
				t.Fatal(err)
			}
			checkConsistent(t, m)
			if m.Invertible() {
				b := make([]big.Rat, n)
				for j := range b {
					b[j].SetInt64(int64(rng.Intn(7) - 3))
				}
				res, err := m.Solve(b)
				if err != nil {
					t.Fatal(err)
				}
				if res.Status != Unique {
					t.Fatalf("可逆矩阵应返回 Unique，得到 %v", res.Status)
				}
				if got := matVec(snapshot(m), res.Particular); !vecEq(got, b) {
					t.Fatalf("解代回失败: Ax=%v, b=%v", got, b)
				}
			}
		}
	}
}

// TestSingularAndRecover 可逆 → 奇异 → 恢复，全程与全量消元核对。
func TestSingularAndRecover(t *testing.T) {
	m, err := NewFromRows(rowsFromInts(
		[]int64{2, 0, 0},
		[]int64{0, 3, 0},
		[]int64{0, 0, 5},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Det(); got.Cmp(big.NewRat(30, 1)) != 0 {
		t.Fatalf("det = %v, 期望 30", &got)
	}
	// 替换第 2 行使之与第 0 行成比例 → Sherman–Morrison 分母为零 → 可靠路径。
	if err := m.SetRow(2, ratsFromInts(4, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if m.Invertible() {
		t.Fatal("应为奇异")
	}
	if got := m.Det(); got.Sign() != 0 {
		t.Fatalf("奇异矩阵 det = %v, 期望 0", &got)
	}
	checkConsistent(t, m)
	// 奇异状态下继续替换 → 恢复可逆。
	if err := m.SetRow(2, ratsFromInts(1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if !m.Invertible() {
		t.Fatal("应恢复可逆")
	}
	checkConsistent(t, m)
	// 再次替换回奇异（全零行）。
	if err := m.SetRow(1, ratsFromInts(0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if d := m.Det(); m.Invertible() || d.Sign() != 0 {
		t.Fatal("应为奇异且 det = 0")
	}
	checkConsistent(t, m)
}

// TestInconsistentCertificate 无解时返回证据 y：yᵀA = 0 且 yᵀb ≠ 0。
func TestInconsistentCertificate(t *testing.T) {
	m, err := NewFromRows(rowsFromInts(
		[]int64{1, 1},
		[]int64{2, 2},
	))
	if err != nil {
		t.Fatal(err)
	}
	b := ratsFromInts(1, 3)
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Inconsistent {
		t.Fatalf("应为 Inconsistent，得到 %v", res.Status)
	}
	y := res.Certificate
	a := snapshot(m)
	// yᵀA 应为零向量。
	col := make([]big.Rat, 2)
	for j := 0; j < 2; j++ {
		acc := new(big.Rat)
		for i := 0; i < 2; i++ {
			acc.Add(acc, new(big.Rat).Mul(&y[i], &a[i][j]))
		}
		col[j].Set(acc)
	}
	if !vecIsZero(col) {
		t.Fatalf("证据无效: yᵀA = %v ≠ 0", col)
	}
	// yᵀb 应非零。
	dot := new(big.Rat)
	for i := 0; i < 2; i++ {
		dot.Add(dot, new(big.Rat).Mul(&y[i], &b[i]))
	}
	if dot.Sign() == 0 {
		t.Fatal("证据无效: yᵀb = 0")
	}
}

// TestUnderdetermined 欠定时：特解可代回，零空间基被 A 零化且线性无关。
func TestUnderdetermined(t *testing.T) {
	m, err := NewFromRows(rowsFromInts(
		[]int64{1, 2, 3},
		[]int64{2, 4, 6},
		[]int64{1, 1, 1},
	))
	if err != nil {
		t.Fatal(err)
	}
	b := ratsFromInts(6, 12, 2)
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Underdetermined {
		t.Fatalf("应为 Underdetermined，得到 %v", res.Status)
	}
	a := snapshot(m)
	if got := matVec(a, res.Particular); !vecEq(got, b) {
		t.Fatalf("特解代回失败: Ax=%v, b=%v", got, b)
	}
	if len(res.NullBasis) != 1 {
		t.Fatalf("零空间维数 = %d, 期望 1", len(res.NullBasis))
	}
	for _, v := range res.NullBasis {
		if got := matVec(a, v); !vecIsZero(got) {
			t.Fatalf("零空间基向量未被零化: Av=%v", got)
		}
	}
	// 特解 + 任意基组合仍是解（随机系数代回验证）。
	combo := make([]big.Rat, 3)
	copy(combo, res.Particular)
	c := big.NewRat(-7, 3)
	for j := range combo {
		combo[j].Add(&combo[j], new(big.Rat).Mul(c, &res.NullBasis[0][j]))
	}
	if got := matVec(a, combo); !vecEq(got, b) {
		t.Fatalf("组合解代回失败: Ax=%v, b=%v", got, b)
	}
}

// TestHilbertExact 4 阶 Hilbert 矩阵行列式为 1/6048000：
// 浮点意义上“接近零”，但精确算术中非零，必须判为可逆。
func TestHilbertExact(t *testing.T) {
	n := 4
	rows := make([][]big.Rat, n)
	for i := range rows {
		rows[i] = make([]big.Rat, n)
		for j := range rows[i] {
			rows[i][j].SetFrac64(1, int64(i+j+1))
		}
	}
	m, err := NewFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Invertible() {
		t.Fatal("Hilbert 矩阵必须判为可逆（行列式小但精确非零）")
	}
	if got, want := m.Det(), *big.NewRat(1, 6048000); got.Cmp(&want) != 0 {
		t.Fatalf("det = %v, 期望 %v", &got, &want)
	}
	// 替换一行为分数组合后仍可逆，且 Solve 的解可代回。
	nr := make([]big.Rat, n)
	for j := range nr {
		nr[j].SetFrac64(int64(j+1), int64(j+2))
	}
	if err := m.SetRow(0, nr); err != nil {
		t.Fatal(err)
	}
	checkConsistent(t, m)
	b := ratsFromInts(1, 0, 0, 1)
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Unique {
		t.Fatalf("应为 Unique，得到 %v", res.Status)
	}
	if got := matVec(snapshot(m), res.Particular); !vecEq(got, b) {
		t.Fatalf("解代回失败: Ax=%v, b=%v", got, b)
	}
}

// TestSingularSolveConsistentUnique 奇异但方阵秩满只可能可逆；
// 这里验证奇异且相容时给出欠定结果（秩亏的极端情形：单行矩阵）。
func TestSingularSolveConsistentUnique(t *testing.T) {
	m, err := NewFromRows(rowsFromInts([]int64{2}))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetRow(0, ratsFromInts(0)); err != nil {
		t.Fatal(err)
	}
	res, err := m.Solve(ratsFromInts(0))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Underdetermined || len(res.NullBasis) != 1 {
		t.Fatalf("0x = 0 应为欠定且零空间维数 1，得到 %v / %d", res.Status, len(res.NullBasis))
	}
	res, err = m.Solve(ratsFromInts(1))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Inconsistent {
		t.Fatalf("0x = 1 应为无解，得到 %v", res.Status)
	}
}
