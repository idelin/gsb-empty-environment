package main

import "math/big"

// identityMat returns the n x n identity matrix.
func identityMat(n int) [][]*big.Int {
	m := make([][]*big.Int, n)
	for i := range m {
		m[i] = make([]*big.Int, n)
		for j := range m[i] {
			m[i][j] = new(big.Int)
		}
		m[i][i].SetInt64(1)
	}
	return m
}

// copyMat deep-copies a matrix.
func copyMat(A [][]*big.Int) [][]*big.Int {
	B := make([][]*big.Int, len(A))
	for i := range A {
		B[i] = make([]*big.Int, len(A[i]))
		for j := range A[i] {
			B[i][j] = new(big.Int).Set(A[i][j])
		}
	}
	return B
}

func cloneVec(v []*big.Int) []*big.Int {
	w := make([]*big.Int, len(v))
	for i := range v {
		w[i] = new(big.Int).Set(v[i])
	}
	return w
}

func swapRows(A [][]*big.Int, i, j int) { A[i], A[j] = A[j], A[i] }

func swapCols(A [][]*big.Int, i, j int) {
	for r := range A {
		A[r][i], A[r][j] = A[r][j], A[r][i]
	}
}

// rowSubMul: row dst -= q * row src.
func rowSubMul(A [][]*big.Int, dst, src int, q *big.Int) {
	tmp := new(big.Int)
	for j := range A[dst] {
		tmp.Mul(q, A[src][j])
		A[dst][j].Sub(A[dst][j], tmp)
	}
}

// colSubMul: column dst -= q * column src.
func colSubMul(A [][]*big.Int, dst, src int, q *big.Int) {
	tmp := new(big.Int)
	for i := range A {
		tmp.Mul(q, A[i][src])
		A[i][dst].Sub(A[i][dst], tmp)
	}
}

// matVec computes A * x.
func matVec(A [][]*big.Int, x []*big.Int) []*big.Int {
	out := make([]*big.Int, len(A))
	tmp := new(big.Int)
	for i := range A {
		out[i] = new(big.Int)
		for j := range A[i] {
			tmp.Mul(A[i][j], x[j])
			out[i].Add(out[i], tmp)
		}
	}
	return out
}

// floorDiv returns floor(a/b) for b != 0.
func floorDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(a, b, r)
	if r.Sign() != 0 && (r.Sign() < 0) != (b.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// ceilDiv returns ceil(a/b) for b != 0.
func ceilDiv(a, b *big.Int) *big.Int {
	q := floorDiv(new(big.Int).Neg(a), b)
	return q.Neg(q)
}

// floorRat returns floor(r).
func floorRat(r *big.Rat) *big.Int {
	return floorDiv(r.Num(), r.Denom())
}

// ceilRat returns ceil(r).
func ceilRat(r *big.Rat) *big.Int {
	q := floorRat(new(big.Rat).Neg(r))
	return q.Neg(q)
}
