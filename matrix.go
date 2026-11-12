package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Rational 是以精确分数表示的概率权重，Denominator 必须为正数。
// 一行内各 Rational 的分子除以各自分母之和必须恰好等于 1。
type Rational struct {
	Numerator   int64
	Denominator int64
}

// Matrix 是一个 n×n 的行随机矩阵：Row[from][to] 表示从符号 from 转移到
// 符号 to 的概率。符号取值为 [0, n)。
type Matrix struct {
	Row [][]Rational
}

var (
	ErrEmptyMatrix      = errors.New("netsec: matrix must be non-empty and square")
	ErrRaggedMatrix     = errors.New("netsec: matrix must be square")
	ErrBadDenominator   = errors.New("netsec: rational denominator must be positive")
	ErrNegativeEntry    = errors.New("netsec: rational numerator must be non-negative")
	ErrRowNotNormalized = errors.New("netsec: matrix row probabilities must sum to 1")
	ErrSymbolOutOfRange = errors.New("netsec: symbol outside matrix alphabet")
	ErrWeightsTooLarge  = errors.New("netsec: resolved integer weights overflow int64")
)

// Size 返回矩阵的字母表大小。
func (m Matrix) Size() int { return len(m.Row) }

// Validate 校验矩阵为方阵、概率合法且每行归一化。
func (m Matrix) Validate() error {
	n := len(m.Row)
	if n == 0 {
		return ErrEmptyMatrix
	}
	one := big.NewRat(1, 1)
	for i := range m.Row {
		if len(m.Row[i]) != n {
			return fmt.Errorf("%w: row %d has width %d, want %d", ErrRaggedMatrix, i, len(m.Row[i]), n)
		}
		sum := new(big.Rat)
		for _, r := range m.Row[i] {
			if r.Denominator <= 0 {
				return fmt.Errorf("%w: row %d", ErrBadDenominator, i)
			}
			if r.Numerator < 0 {
				return fmt.Errorf("%w: row %d", ErrNegativeEntry, i)
			}
			sum.Add(sum, big.NewRat(r.Numerator, r.Denominator))
		}
		if sum.Cmp(one) != 0 {
			return fmt.Errorf("%w: row %d sums to %s", ErrRowNotNormalized, i, sum.RatString())
		}
	}
	return nil
}

// resolvedRow 把一行有理概率解析为整数权重：以该行分母的最小公倍数 T 作为
// 抽取总权重，每个符号的权重为该有理概率乘以 T。
type resolvedRow struct {
	weights []int64
	total   int64
}

func resolveRow(row []Rational) (resolvedRow, error) {
	denoms := make([]int64, len(row))
	for i, r := range row {
		denoms[i] = r.Denominator
	}
	total, err := lcm(denoms)
	if err != nil {
		return resolvedRow{}, err
	}
	weights := make([]int64, len(row))
	for i, r := range row {
		// total 是 r.Denominator 的倍数，整除无余数。
		q := total / r.Denominator
		w, overflow := mul64(r.Numerator, q)
		if overflow {
			return resolvedRow{}, ErrWeightsTooLarge
		}
		weights[i] = w
	}
	return resolvedRow{weights: weights, total: total}, nil
}

// draw 按整数权重做一次抽取，r 提供 [0,total) 上的均匀整数。
func (rr resolvedRow) draw(r Source) int {
	var cumulative int64
	x := int64(r.Intn(int(rr.total)))
	for symbol, w := range rr.weights {
		cumulative += w
		if x < cumulative {
			return symbol
		}
	}
	// 权重之和等于 total 且 x < total，正常情况下不可达。
	return len(rr.weights) - 1
}

// lcm 返回一组正整数的最小公倍数，超过 int64 范围时报错。
func lcm(values []int64) (int64, error) {
	result := int64(1)
	for _, v := range values {
		g := new(big.Int).GCD(nil, nil, big.NewInt(result), big.NewInt(v)).Int64()
		next, overflow := mul64(result/g, v)
		if overflow {
			return 0, ErrWeightsTooLarge
		}
		result = next
	}
	return result, nil
}

func mul64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	r := a * b
	if r/a != b {
		return 0, true
	}
	return r, false
}
