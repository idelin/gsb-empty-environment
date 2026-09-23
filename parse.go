package main

import "bytes"

// Token cost model, matching the stream format exactly for runs of at most
// maxLitRunLen bytes and dictionaries of at most maxDictEntries entries:
//
//	literal run: 1 tag byte + 1-byte varint length + raw bytes
//	match:       1 tag byte + 1-byte varint index
const (
	tagLiteralRun  = 0
	tagMatch       = 1
	maxLitRunLen   = 127
	maxDictEntries = 128

	litRunHeaderCost = 2
	matchTokenCost   = 2
)

// token is one parse decision: a dictionary match (match >= 0) or a literal
// run (match < 0).
type token struct {
	match   int
	literal []byte
}

// dictIndex groups dictionary entries by first byte for fast prefix lookup.
type dictIndex struct {
	dict    [][]byte
	byFirst [256][]int
}

func buildDictIndex(dict [][]byte) *dictIndex {
	di := &dictIndex{dict: dict}
	for i, p := range dict {
		if len(p) > 0 {
			di.byFirst[p[0]] = append(di.byFirst[p[0]], i)
		}
	}
	return di
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fastParseCost returns the optimal parse cost under the token cost model,
// treating a literal run of any length as a single run. Runs longer than
// maxLitRunLen are split by the encoder, adding 2 bytes per extra run; use
// exactParseCost when that distinction matters (exact selection mode).
func fastParseCost(s []byte, di *dictIndex) int {
	n := len(s)
	g := make([]int, n+1) // no literal run open at i
	f := make([]int, n+1) // a literal run is open at i
	for i := n - 1; i >= 0; i-- {
		f[i] = 1 + minInt(f[i+1], g[i+1])
		best := litRunHeaderCost + f[i]
		for _, pi := range di.byFirst[s[i]] {
			if p := di.dict[pi]; bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + g[i+len(p)]; c < best {
					best = c
				}
			}
		}
		g[i] = best
	}
	return g[0]
}

// fastParse returns an optimal parse under the same (slightly simplified) cost
// model as fastParseCost.
func fastParse(s []byte, di *dictIndex) []token {
	n := len(s)
	g := make([]int, n+1)
	f := make([]int, n+1)
	stay := make([]bool, n)   // after a literal at i, keep the run open
	matchAt := make([]int, n) // in state g at i: chosen phrase, -1 = literal
	for i := n - 1; i >= 0; i-- {
		stay[i] = f[i+1] <= g[i+1]
		f[i] = 1 + minInt(f[i+1], g[i+1])
		best := litRunHeaderCost + f[i]
		matchAt[i] = -1
		for _, pi := range di.byFirst[s[i]] {
			if p := di.dict[pi]; bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + g[i+len(p)]; c < best {
					best = c
					matchAt[i] = pi
				}
			}
		}
		g[i] = best
	}
	var toks []token
	for i := 0; i < n; {
		if matchAt[i] >= 0 {
			toks = append(toks, token{match: matchAt[i]})
			i += len(di.dict[matchAt[i]])
			continue
		}
		start := i
		for {
			open := stay[i]
			i++
			if i >= n || !open {
				break
			}
		}
		toks = append(toks, token{match: -1, literal: s[start:i]})
	}
	return toks
}

// exactParseCost is the exact minimal encoded size of s in the real stream
// format, where a literal run may hold at most maxLitRunLen bytes. It uses a
// DP over (position, open run length) and costs O(n * maxLitRunLen) memory,
// so it is only used for small inputs in exact selection mode.
func exactParseCost(s []byte, di *dictIndex) int {
	n := len(s)
	const w = maxLitRunLen + 1
	dp := make([]int, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		row := dp[i*w : (i+1)*w]
		next := dp[(i+1)*w : (i+2)*w]
		best0 := litRunHeaderCost + 1 + next[1]
		for _, pi := range di.byFirst[s[i]] {
			if p := di.dict[pi]; bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + dp[(i+len(p))*w]; c < best0 {
					best0 = c
				}
			}
		}
		row[0] = best0
		for r := 1; r < maxLitRunLen; r++ {
			row[r] = minInt(1+next[r+1], row[0])
		}
		row[maxLitRunLen] = row[0]
	}
	return dp[0]
}

// exactParse returns an exact-optimal token parse (same DP as exactParseCost).
func exactParse(s []byte, di *dictIndex) []token {
	n := len(s)
	const w = maxLitRunLen + 1
	dp := make([]int, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		row := dp[i*w : (i+1)*w]
		next := dp[(i+1)*w : (i+2)*w]
		best0 := litRunHeaderCost + 1 + next[1]
		for _, pi := range di.byFirst[s[i]] {
			if p := di.dict[pi]; bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + dp[(i+len(p))*w]; c < best0 {
					best0 = c
				}
			}
		}
		row[0] = best0
		for r := 1; r < maxLitRunLen; r++ {
			row[r] = minInt(1+next[r+1], row[0])
		}
		row[maxLitRunLen] = row[0]
	}
	var toks []token
	for i := 0; i < n; {
		// Recompute the best option at (i, 0) in deterministic order:
		// literal first, then matches by entry index.
		best := litRunHeaderCost + 1 + dp[(i+1)*w+1]
		chosen := -1
		for _, pi := range di.byFirst[s[i]] {
			if p := di.dict[pi]; bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + dp[(i+len(p))*w]; c < best {
					best = c
					chosen = pi
				}
			}
		}
		if chosen >= 0 {
			toks = append(toks, token{match: chosen})
			i += len(di.dict[chosen])
			continue
		}
		// Open a literal run; keep consuming while continuing is optimal.
		start := i
		i++
		for r := 1; i < n && r < maxLitRunLen; r++ {
			if 1+dp[(i+1)*w+r+1] > dp[i*w+r] {
				break
			}
			i++
		}
		toks = append(toks, token{match: -1, literal: s[start:i]})
	}
	return toks
}
