package netsec

import (
	"encoding/hex"
	"errors"
)

// QueryView is one deterministic realization of the vector sent to each
// server: q0 to the first server and q1 = q0 XOR e_j to the second.
type QueryView struct {
	Q0 []byte
	Q1 []byte
}

// EnumerateQueryViews enumerates all 2^n possible q0 values for n <= 8 and
// returns the corresponding views. It is intended for the distribution audit,
// not for query execution.
func EnumerateQueryViews(n, j int) ([]QueryView, error) {
	if n < 1 || n > 8 {
		return nil, errors.New("pir: enumeration requires 1 <= N <= 8")
	}
	if j < 0 || j >= n {
		return nil, errors.New("pir: index out of range")
	}

	total := 1 << uint(n)
	views := make([]QueryView, total)
	for value := 0; value < total; value++ {
		q0 := make([]byte, QueryLength(n))
		for bit := 0; bit < n; bit++ {
			if value&(1<<uint(bit)) != 0 {
				q0[bit/8] |= byte(1 << uint(bit%8))
			}
		}
		q1 := append([]byte(nil), q0...)
		q1[j/8] ^= byte(1 << uint(j%8))
		views[value] = QueryView{Q0: q0, Q1: q1}
	}
	return views, nil
}

// ViewDistribution records how often each distinct vector is sent to each
// server when q0 ranges uniformly over all 2^n vectors.
type ViewDistribution struct {
	N       int
	Index   int
	Total   int
	ServerA map[string]int
	ServerB map[string]int
}

// ComputeViewDistribution enumerates all q0 values and counts the vectors
// observed by each server. Hex encoding makes the report printable.
func ComputeViewDistribution(n, j int) (ViewDistribution, error) {
	views, err := EnumerateQueryViews(n, j)
	if err != nil {
		return ViewDistribution{}, err
	}

	report := ViewDistribution{
		N:       n,
		Index:   j,
		Total:   len(views),
		ServerA: make(map[string]int, len(views)),
		ServerB: make(map[string]int, len(views)),
	}
	for _, view := range views {
		report.ServerA[hex.EncodeToString(view.Q0)]++
		report.ServerB[hex.EncodeToString(view.Q1)]++
	}
	return report, nil
}
