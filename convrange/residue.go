package convrange

import "math/big"

// blockResidues computes c_k mod p for k in [lo,hi), choosing between a
// dense scan and a sparse nonzero-pair pass by estimated operation count.
func blockResidues(a, b []*big.Int, nza, nzb []nonzeroTerm, p uint64, lo, hi int) []uint64 {
	out := make([]uint64, hi-lo)
	denseOps := int64(hi-lo) * int64(min(len(a), len(b)))
	sparseOps := int64(len(nza)) * int64(len(nzb))
	if sparseOps < denseOps {
		sparseResidues(nza, nzb, p, lo, hi, out)
	} else {
		denseResidues(a, b, p, lo, hi, out)
	}
	return out
}

// denseResidues evaluates c_k = sum_i a_i*b_{k-i} (mod p) directly.
func denseResidues(a, b []*big.Int, p uint64, lo, hi int, out []uint64) {
	bp := big.NewInt(int64(p))
	am := make([]uint64, len(a))
	for i, c := range a {
		am[i] = modPos(c, bp, p)
	}
	bm := make([]uint64, len(b))
	for j, c := range b {
		bm[j] = modPos(c, bp, p)
	}
	for k := lo; k < hi; k++ {
		iLo := k - (len(b) - 1)
		if iLo < 0 {
			iLo = 0
		}
		iHi := k
		if iHi > len(a)-1 {
			iHi = len(a) - 1
		}
		var acc uint64
		for i := iLo; i <= iHi; i++ {
			// am[i], bm[k-i] < p < 2^31, so the product is < 2^62 and
			// acc + product < 2^63: no uint64 overflow.
			acc = (acc + am[i]*bm[k-i]) % p
		}
		out[k-lo] = acc
	}
}

// sparseResidues accumulates only nonzero*nonzero term pairs.
func sparseResidues(nza, nzb []nonzeroTerm, p uint64, lo, hi int, out []uint64) {
	bp := big.NewInt(int64(p))
	am := make([]uint64, len(nza))
	for i, t := range nza {
		am[i] = modPos(t.val, bp, p)
	}
	bm := make([]uint64, len(nzb))
	for j, t := range nzb {
		bm[j] = modPos(t.val, bp, p)
	}
	for i, ta := range nza {
		av := am[i]
		for j, tb := range nzb {
			k := ta.idx + tb.idx
			if k >= lo && k < hi {
				out[k-lo] = (out[k-lo] + av*bm[j]) % p
			}
		}
	}
}

// modPos returns c mod p in [0, p) for any signed c.
func modPos(c, bp *big.Int, p uint64) uint64 {
	if c == nil {
		return 0
	}
	r := new(big.Int).Mod(c, bp) // big.Int.Mod returns non-negative for bp>0
	return r.Uint64()
}
