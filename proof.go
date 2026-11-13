package netsec

// Proof is a logarithmic-size inner product proof: one L/R pair per
// folding round plus the final single-element scalars.
type Proof struct {
	L []Point
	R []Point
	A Scalar
	B Scalar
}

// Marshal serializes the proof as the concatenation of canonical
// encodings: L[0]||R[0]||...||L[k-1]||R[k-1]||A||B. The round count is
// not stored; it is derived from n during verification.
func (p *Proof) Marshal(g Group) []byte {
	ps, ss := g.PointSize(), g.ScalarSize()
	out := make([]byte, 0, len(p.L)*2*ps+2*ss)
	for i := range p.L {
		out = append(out, p.L[i].Bytes()...)
		out = append(out, p.R[i].Bytes()...)
	}
	out = append(out, p.A.Bytes()...)
	out = append(out, p.B.Bytes()...)
	return out
}

// UnmarshalProof decodes a proof produced by Marshal. Every scalar and
// point encoding is checked for canonical form by the group adapter;
// truncated or over-long inputs are rejected.
func UnmarshalProof(g Group, data []byte) (*Proof, error) {
	ps, ss := g.PointSize(), g.ScalarSize()
	if ps <= 0 || ss <= 0 {
		return nil, ErrEncoding
	}
	if len(data) < 2*ss {
		return nil, ErrEncoding
	}
	rem := len(data) - 2*ss
	if rem%(2*ps) != 0 {
		return nil, ErrEncoding
	}
	rounds := rem / (2 * ps)
	p := &Proof{
		L: make([]Point, rounds),
		R: make([]Point, rounds),
	}
	off := 0
	for i := 0; i < rounds; i++ {
		l, err := g.PointFromBytes(data[off : off+ps])
		if err != nil {
			return nil, err
		}
		off += ps
		r, err := g.PointFromBytes(data[off : off+ps])
		if err != nil {
			return nil, err
		}
		off += ps
		p.L[i], p.R[i] = l, r
	}
	a, err := g.ScalarFromBytes(data[off : off+ss])
	if err != nil {
		return nil, err
	}
	off += ss
	b, err := g.ScalarFromBytes(data[off : off+ss])
	if err != nil {
		return nil, err
	}
	p.A, p.B = a, b
	return p, nil
}
