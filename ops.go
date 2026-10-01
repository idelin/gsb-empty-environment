package crset

// Set operations work across mixed representations: containers are combined
// through their iterators and per-representation contains(), and results are
// re-normalized into the cheapest encoding under the receiver's config.

func (s *Set) appendBucket(high uint16, c *container) {
	if c.card == 0 {
		return
	}
	s.buckets = append(s.buckets, bucket{high: high, c: c})
	s.card += c.card
}

// Union returns a new set with all elements of both sets.
func (a *Set) Union(b *Set) *Set {
	out := &Set{cfg: a.cfg}
	ia, ib := 0, 0
	for ia < len(a.buckets) && ib < len(b.buckets) {
		ha, hb := a.buckets[ia].high, b.buckets[ib].high
		switch {
		case ha < hb:
			out.appendBucket(ha, a.buckets[ia].c.clone())
			ia++
		case hb < ha:
			out.appendBucket(hb, b.buckets[ib].c.clone())
			ib++
		default:
			out.appendBucket(ha, unionContainers(a.buckets[ia].c, b.buckets[ib].c, a.cfg, &out.conversions))
			ia++
			ib++
		}
	}
	for ; ia < len(a.buckets); ia++ {
		out.appendBucket(a.buckets[ia].high, a.buckets[ia].c.clone())
	}
	for ; ib < len(b.buckets); ib++ {
		out.appendBucket(b.buckets[ib].high, b.buckets[ib].c.clone())
	}
	return out
}

// Intersect returns a new set with elements present in both sets.
func (a *Set) Intersect(b *Set) *Set {
	out := &Set{cfg: a.cfg}
	ia, ib := 0, 0
	for ia < len(a.buckets) && ib < len(b.buckets) {
		ha, hb := a.buckets[ia].high, b.buckets[ib].high
		switch {
		case ha < hb:
			ia++
		case hb < ha:
			ib++
		default:
			out.appendBucket(ha, intersectContainers(a.buckets[ia].c, b.buckets[ib].c, a.cfg, &out.conversions))
			ia++
			ib++
		}
	}
	return out
}

// Difference returns a new set with elements of a not present in b.
func (a *Set) Difference(b *Set) *Set {
	out := &Set{cfg: a.cfg}
	ib := 0
	for ia := 0; ia < len(a.buckets); ia++ {
		ha := a.buckets[ia].high
		for ib < len(b.buckets) && b.buckets[ib].high < ha {
			ib++
		}
		if ib < len(b.buckets) && b.buckets[ib].high == ha {
			out.appendBucket(ha, differenceContainers(a.buckets[ia].c, b.buckets[ib].c, a.cfg, &out.conversions))
		} else {
			out.appendBucket(ha, a.buckets[ia].c.clone())
		}
	}
	return out
}

// SymDifference returns a new set with elements present in exactly one set.
func (a *Set) SymDifference(b *Set) *Set {
	out := &Set{cfg: a.cfg}
	ia, ib := 0, 0
	for ia < len(a.buckets) && ib < len(b.buckets) {
		ha, hb := a.buckets[ia].high, b.buckets[ib].high
		switch {
		case ha < hb:
			out.appendBucket(ha, a.buckets[ia].c.clone())
			ia++
		case hb < ha:
			out.appendBucket(hb, b.buckets[ib].c.clone())
			ib++
		default:
			out.appendBucket(ha, symDiffContainers(a.buckets[ia].c, b.buckets[ib].c, a.cfg, &out.conversions))
			ia++
			ib++
		}
	}
	for ; ia < len(a.buckets); ia++ {
		out.appendBucket(a.buckets[ia].high, a.buckets[ia].c.clone())
	}
	for ; ib < len(b.buckets); ib++ {
		out.appendBucket(b.buckets[ib].high, b.buckets[ib].c.clone())
	}
	return out
}

func unionContainers(a, b *container, cfg Config, conv *uint64) *container {
	capHint := a.card + b.card
	if capHint > 65536 {
		capHint = 65536
	}
	out := &container{rep: repSparse, sparse: make([]uint16, 0, capHint)}
	ia, ib := a.iter(), b.iter()
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka || okb {
		switch {
		case !okb || (oka && va < vb):
			out.sparse = append(out.sparse, va)
			va, oka = ia.next()
		case !oka || vb < va:
			out.sparse = append(out.sparse, vb)
			vb, okb = ib.next()
		default:
			out.sparse = append(out.sparse, va)
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	out.card = len(out.sparse)
	if out.normalize(cfg) && conv != nil {
		*conv++
	}
	return out
}

func intersectContainers(a, b *container, cfg Config, conv *uint64) *container {
	src, other := a, b
	if b.card < a.card {
		src, other = b, a
	}
	out := &container{rep: repSparse, sparse: make([]uint16, 0, src.card)}
	it := src.iter()
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		if other.contains(v) {
			out.sparse = append(out.sparse, v)
		}
	}
	out.card = len(out.sparse)
	if out.normalize(cfg) && conv != nil {
		*conv++
	}
	return out
}

func differenceContainers(a, b *container, cfg Config, conv *uint64) *container {
	out := &container{rep: repSparse, sparse: make([]uint16, 0, a.card)}
	it := a.iter()
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		if !b.contains(v) {
			out.sparse = append(out.sparse, v)
		}
	}
	out.card = len(out.sparse)
	if out.normalize(cfg) && conv != nil {
		*conv++
	}
	return out
}

func symDiffContainers(a, b *container, cfg Config, conv *uint64) *container {
	out := &container{rep: repSparse, sparse: make([]uint16, 0, a.card+b.card)}
	ia, ib := a.iter(), b.iter()
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka || okb {
		switch {
		case !okb || (oka && va < vb):
			out.sparse = append(out.sparse, va)
			va, oka = ia.next()
		case !oka || vb < va:
			out.sparse = append(out.sparse, vb)
			vb, okb = ib.next()
		default:
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	out.card = len(out.sparse)
	if out.normalize(cfg) && conv != nil {
		*conv++
	}
	return out
}
