// Package exactmatrix 提供基于 big.Rat 的精确方阵运算：
// 支持反复替换任意一行并增量维护行列式，可逆时提供求解接口；
// 矩阵可从可逆变奇异再恢复，低秩更新公式分母为零时自动切换全量精确消元。
package exactmatrix

import (
	"errors"
	"fmt"
	"math/big"
)

// Status 表示 Solve 的结果类别。
type Status int

const (
	// Unique 唯一解，Particular 即解。
	Unique Status = iota
	// Underdetermined 欠定：无穷多解。Particular 为一特解，
	// NullBasis 为零空间一组基，通解 = Particular + NullBasis 的任意线性组合。
	Underdetermined
	// Inconsistent 无解。Certificate 为证据向量 y：yᵀA = 0 且 yᵀb ≠ 0。
	Inconsistent
)

func (s Status) String() string {
	switch s {
	case Unique:
		return "Unique"
	case Underdetermined:
		return "Underdetermined"
	case Inconsistent:
		return "Inconsistent"
	}
	return "Unknown"
}

// SolveResult 是 Solve 的返回。所有解均为精确有理数，可直接代回验证。
type SolveResult struct {
	Status Status
	// Particular 唯一解或一个特解（Status 为 Unique / Underdetermined 时非 nil）。
	Particular []big.Rat
	// NullBasis 零空间基（Status 为 Underdetermined 时非 nil，线性无关）。
	NullBasis [][]big.Rat
	// Certificate 无解证据 y：yᵀA = 0 而 yᵀb ≠ 0（Status 为 Inconsistent 时非 nil）。
	Certificate []big.Rat
}

// Matrix 是 n×n 精确有理数方阵，内部缓存行列式，可逆时同时缓存逆矩阵。
type Matrix struct {
	n   int
	a   [][]big.Rat
	det big.Rat
	inv [][]big.Rat // 非 nil 当且仅当当前矩阵可逆
}

// Identity 返回 n 阶单位矩阵。
func Identity(n int) (*Matrix, error) {
	if n < 1 {
		return nil, errors.New("exactmatrix: 阶数必须 >= 1")
	}
	a := make([][]big.Rat, n)
	for i := range a {
		a[i] = make([]big.Rat, n)
		a[i][i].SetInt64(1)
	}
	m := &Matrix{n: n, a: a}
	m.recompute()
	return m, nil
}

// NewFromRows 由给定行构造矩阵，行数必须等于每行长度。
func NewFromRows(rows [][]big.Rat) (*Matrix, error) {
	n := len(rows)
	if n < 1 {
		return nil, errors.New("exactmatrix: 阶数必须 >= 1")
	}
	for i, r := range rows {
		if len(r) != n {
			return nil, fmt.Errorf("exactmatrix: 第 %d 行长度为 %d，期望 %d", i, len(r), n)
		}
	}
	m := &Matrix{n: n, a: cloneRows(rows)}
	m.recompute()
	return m, nil
}

// N 返回阶数。
func (m *Matrix) N() int { return m.n }

// Invertible 报告当前矩阵是否可逆（精确判定：det ≠ 0）。
func (m *Matrix) Invertible() bool { return m.inv != nil }

// Det 返回当前行列式的副本。
func (m *Matrix) Det() big.Rat { return *new(big.Rat).Set(&m.det) }

// At 返回元素 (i,j) 的副本。
func (m *Matrix) At(i, j int) big.Rat { return *new(big.Rat).Set(&m.a[i][j]) }

// Row 返回第 i 行的副本。
func (m *Matrix) Row(i int) []big.Rat {
	r := make([]big.Rat, m.n)
	for j := range r {
		r[j].Set(&m.a[i][j])
	}
	return r
}

// Inverse 返回逆矩阵的深拷贝；奇异时返回 nil。
func (m *Matrix) Inverse() [][]big.Rat {
	if m.inv == nil {
		return nil
	}
	return cloneRows(m.inv)
}

// SetRow 用 row 替换第 i 行，并维护行列式（以及可逆时的逆矩阵）。
//
// 快速路径：当前可逆时，用 Sherman–Morrison 公式
//
//	s = row · (A⁻¹ 的第 i 列)，det' = det·s，
//	A'⁻¹ = A⁻¹ − (A⁻¹eᵢ)((row−aᵢ)ᵀA⁻¹)/s。
//
// 当 s == 0（分母为零，新矩阵奇异）或当前本就奇异时，
// 切换可靠路径：对更新后的矩阵做全量精确消元，重建 det / inv 状态。
func (m *Matrix) SetRow(i int, row []big.Rat) error {
	if i < 0 || i >= m.n {
		return fmt.Errorf("exactmatrix: 行下标 %d 越界", i)
	}
	if len(row) != m.n {
		return fmt.Errorf("exactmatrix: 新行长度为 %d，期望 %d", len(row), m.n)
	}
	if m.inv != nil {
		// s = Σ_k row[k]·inv[k][i]
		s := new(big.Rat)
		for k := 0; k < m.n; k++ {
			s.Add(s, new(big.Rat).Mul(&row[k], &m.inv[k][i]))
		}
		if s.Sign() != 0 {
			// u = A⁻¹eᵢ（inv 的第 i 列）；w = (row−aᵢ)ᵀA⁻¹
			u := make([]big.Rat, m.n)
			w := make([]big.Rat, m.n)
			for k := 0; k < m.n; k++ {
				u[k].Set(&m.inv[k][i])
			}
			for j := 0; j < m.n; j++ {
				acc := new(big.Rat)
				for k := 0; k < m.n; k++ {
					d := new(big.Rat).Sub(&row[k], &m.a[i][k])
					acc.Add(acc, d.Mul(d, &m.inv[k][j]))
				}
				w[j].Set(acc)
			}
			sInv := new(big.Rat).Inv(s)
			for k := 0; k < m.n; k++ {
				for j := 0; j < m.n; j++ {
					t := new(big.Rat).Mul(&u[k], &w[j])
					t.Mul(t, sInv)
					m.inv[k][j].Sub(&m.inv[k][j], t)
				}
			}
			m.det.Mul(&m.det, s)
			for j := 0; j < m.n; j++ {
				m.a[i][j].Set(&row[j])
			}
			return nil
		}
		// 分母为零：落入可靠路径。
	}
	for j := 0; j < m.n; j++ {
		m.a[i][j].Set(&row[j])
	}
	m.recompute()
	return nil
}

// recompute 可靠路径：全量精确消元，重建 det 与 inv。
func (m *Matrix) recompute() {
	m.det = detElim(m.a)
	if m.det.Sign() != 0 {
		m.inv = inverseElim(m.a)
	} else {
		m.inv = nil
	}
}

// Solve 精确求解 Ax = b。
//
// 可逆时直接返回唯一解 x = A⁻¹b；奇异时对增广矩阵 [A|b|I] 做全量精确消元：
//   - 存在 [0…0 | c≠0] 行 → Inconsistent，该行的 I 块即证据向量 y；
//   - 否则秩 < n → Underdetermined，返回一特解与零空间基。
func (m *Matrix) Solve(b []big.Rat) (SolveResult, error) {
	if len(b) != m.n {
		return SolveResult{}, fmt.Errorf("exactmatrix: 右端长度为 %d，期望 %d", len(b), m.n)
	}
	if m.inv != nil {
		x := make([]big.Rat, m.n)
		for i := 0; i < m.n; i++ {
			acc := new(big.Rat)
			for j := 0; j < m.n; j++ {
				acc.Add(acc, new(big.Rat).Mul(&m.inv[i][j], &b[j]))
			}
			x[i].Set(acc)
		}
		return SolveResult{Status: Unique, Particular: x}, nil
	}
	n := m.n
	aug := make([][]big.Rat, n)
	for i := range aug {
		aug[i] = make([]big.Rat, 2*n+1)
		for j := 0; j < n; j++ {
			aug[i][j].Set(&m.a[i][j])
		}
		aug[i][n].Set(&b[i])
		aug[i][n+1+i].SetInt64(1)
	}
	pivots := rref(aug, n)
	// 无解判定：A 部分全零而 b 部分非零的行。
	for i := len(pivots); i < n; i++ {
		if aug[i][n].Sign() != 0 {
			y := make([]big.Rat, n)
			copy(y, aug[i][n+1:])
			return SolveResult{Status: Inconsistent, Certificate: y}, nil
		}
	}
	// 相容：特解（自由变量取 0）。
	x := make([]big.Rat, n)
	for k, c := range pivots {
		x[c].Set(&aug[k][n])
	}
	if len(pivots) == n {
		return SolveResult{Status: Unique, Particular: x}, nil
	}
	return SolveResult{
		Status:     Underdetermined,
		Particular: x,
		NullBasis:  nullSpace(aug, pivots, n),
	}, nil
}
