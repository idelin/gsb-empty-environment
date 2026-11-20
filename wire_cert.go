package bootpolicy

func writeIntList(w *writer, xs []int) {
	w.u16(uint16(len(xs)))
	for _, x := range xs {
		w.u16(uint16(x))
	}
}

func readIntList(r *reader) ([]int, error) {
	n, err := r.u16Value()
	if err != nil {
		return nil, err
	}
	xs := make([]int, 0, n)
	for i := 0; i < int(n); i++ {
		v, err := r.u16Value()
		if err != nil {
			return nil, err
		}
		xs = append(xs, int(v))
	}
	return xs, nil
}

func writeBool(w *writer, b bool) {
	if b {
		w.byte(1)
	} else {
		w.byte(0)
	}
}

func readBool(r *reader) (bool, error) {
	v, err := r.byteValue()
	if err != nil {
		return false, err
	}
	if v > 1 {
		return false, ErrPackage
	}
	return v == 1, nil
}

func writeCertificate(w *writer, c Certificate) error {
	w.u16(uint16(c.OriginalEntryCount))

	w.u16(uint16(len(c.Prefixes)))
	for _, p := range c.Prefixes {
		if err := w.str(p); err != nil {
			return err
		}
	}
	w.u16(uint16(len(c.PrefixClasses)))
	for _, pc := range c.PrefixClasses {
		if err := w.str(pc.Representative); err != nil {
			return err
		}
		writeBool(w, pc.IsNode)
		if err := w.str(pc.Prefix); err != nil {
			return err
		}
		writeIntList(w, pc.MatchedPrefixes)
	}

	w.u16(uint16(len(c.GenClasses)))
	for _, gc := range c.GenClasses {
		w.u32(gc.Representative)
		w.u32(gc.Lo)
		w.u32(gc.Hi)
		writeBool(w, gc.Tail)
	}

	writeValueClasses(w, c.LocationClasses)

	w.u16(uint16(len(c.DigestClasses)))
	for _, dc := range c.DigestClasses {
		if err := w.str(dc.Representative.Algorithm); err != nil {
			return err
		}
		if err := w.raw(dc.Representative.Hash); err != nil {
			return err
		}
		writeBool(w, dc.Wildcard)
	}
	// Digest matched indexes are encoded in a second pass so wildcard classes
	// with a zero representative are unambiguous.
	w.u16(uint16(len(c.DigestClasses)))
	for _, dc := range c.DigestClasses {
		writeIntList(w, dc.MatchedIndexes)
	}

	writeValueClasses(w, c.RootClasses)

	w.u16(uint16(len(c.TimeClasses)))
	for _, tc := range c.TimeClasses {
		w.u64(tc.Representative)
		w.u64(tc.Lo)
		w.u64(tc.Hi)
		writeBool(w, tc.Tail)
	}

	w.u16(uint16(len(c.ParentClasses)))
	for _, pc := range c.ParentClasses {
		if pc.Scope == nil {
			w.byte(0)
		} else {
			w.byte(1)
			if err := w.str(pc.Scope.SignerID); err != nil {
				return err
			}
			if err := w.str(pc.Scope.Label); err != nil {
				return err
			}
		}
		writeBool(w, pc.NilParent)
		writeBool(w, pc.Wildcard)
		writeIntList(w, pc.MatchedIndexes)
		writeIntList(w, pc.UnscopedIndexes)
	}

	w.u32(uint32(len(c.GroupDenyMasks)))
	for _, m := range c.GroupDenyMasks {
		w.u64(m)
	}
	for _, m := range c.GroupExemptMasks {
		w.u64(m)
	}
	for _, b := range c.GroupOrigDenied {
		writeBool(w, b)
	}
	return nil
}

func writeValueClasses(w *writer, classes []ValueClass) {
	w.u16(uint16(len(classes)))
	for _, vc := range classes {
		_ = w.str(vc.Representative)
		writeBool(w, vc.Wildcard)
		writeIntList(w, vc.MatchedIndexes)
	}
}

func readValueClasses(r *reader) ([]ValueClass, error) {
	n, err := r.u16Value()
	if err != nil {
		return nil, err
	}
	classes := make([]ValueClass, 0, n)
	for i := 0; i < int(n); i++ {
		rep, err := r.strValue()
		if err != nil {
			return nil, err
		}
		wild, err := readBool(r)
		if err != nil {
			return nil, err
		}
		matched, err := readIntList(r)
		if err != nil {
			return nil, err
		}
		classes = append(classes, ValueClass{Representative: rep, Wildcard: wild, MatchedIndexes: matched})
	}
	return classes, nil
}

func readCertificate(r *reader) (Certificate, error) {
	var c Certificate
	nOrig, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.OriginalEntryCount = int(nOrig)

	nPrefix, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.Prefixes = make([]string, 0, nPrefix)
	for i := 0; i < int(nPrefix); i++ {
		v, err := r.strValue()
		if err != nil {
			return c, err
		}
		c.Prefixes = append(c.Prefixes, v)
	}
	nPC, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.PrefixClasses = make([]PrefixClass, 0, nPC)
	for i := 0; i < int(nPC); i++ {
		var pc PrefixClass
		if pc.Representative, err = r.strValue(); err != nil {
			return c, err
		}
		if pc.IsNode, err = readBool(r); err != nil {
			return c, err
		}
		if pc.Prefix, err = r.strValue(); err != nil {
			return c, err
		}
		if pc.MatchedPrefixes, err = readIntList(r); err != nil {
			return c, err
		}
		c.PrefixClasses = append(c.PrefixClasses, pc)
	}

	nGC, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.GenClasses = make([]IntervalClass, 0, nGC)
	for i := 0; i < int(nGC); i++ {
		var gc IntervalClass
		if gc.Representative, err = r.u32Value(); err != nil {
			return c, err
		}
		if gc.Lo, err = r.u32Value(); err != nil {
			return c, err
		}
		if gc.Hi, err = r.u32Value(); err != nil {
			return c, err
		}
		if gc.Tail, err = readBool(r); err != nil {
			return c, err
		}
		c.GenClasses = append(c.GenClasses, gc)
	}

	if c.LocationClasses, err = readValueClasses(r); err != nil {
		return c, err
	}

	nDC, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.DigestClasses = make([]DigestClass, 0, nDC)
	for i := 0; i < int(nDC); i++ {
		var dc DigestClass
		if dc.Representative.Algorithm, err = r.strValue(); err != nil {
			return c, err
		}
		if dc.Representative.Hash, err = r.rawValue(); err != nil {
			return c, err
		}
		if dc.Wildcard, err = readBool(r); err != nil {
			return c, err
		}
		c.DigestClasses = append(c.DigestClasses, dc)
	}
	nDCM, err := r.u16Value()
	if err != nil || int(nDCM) != len(c.DigestClasses) {
		return c, ErrPackage
	}
	for i := range c.DigestClasses {
		if c.DigestClasses[i].MatchedIndexes, err = readIntList(r); err != nil {
			return c, err
		}
	}

	if c.RootClasses, err = readValueClasses(r); err != nil {
		return c, err
	}

	nTC, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.TimeClasses = make([]TimeClass, 0, nTC)
	for i := 0; i < int(nTC); i++ {
		var tc TimeClass
		if tc.Representative, err = r.u64Value(); err != nil {
			return c, err
		}
		if tc.Lo, err = r.u64Value(); err != nil {
			return c, err
		}
		if tc.Hi, err = r.u64Value(); err != nil {
			return c, err
		}
		if tc.Tail, err = readBool(r); err != nil {
			return c, err
		}
		c.TimeClasses = append(c.TimeClasses, tc)
	}

	nPar, err := r.u16Value()
	if err != nil {
		return c, err
	}
	c.ParentClasses = make([]ParentClass, 0, nPar)
	for i := 0; i < int(nPar); i++ {
		var pc ParentClass
		flag, err := r.byteValue()
		if err != nil {
			return c, err
		}
		if flag == 1 {
			pc.Scope = &ParentScope{}
			if pc.Scope.SignerID, err = r.strValue(); err != nil {
				return c, err
			}
			if pc.Scope.Label, err = r.strValue(); err != nil {
				return c, err
			}
		} else if flag != 0 {
			return c, ErrPackage
		}
		if pc.NilParent, err = readBool(r); err != nil {
			return c, err
		}
		if pc.Wildcard, err = readBool(r); err != nil {
			return c, err
		}
		if pc.MatchedIndexes, err = readIntList(r); err != nil {
			return c, err
		}
		if pc.UnscopedIndexes, err = readIntList(r); err != nil {
			return c, err
		}
		c.ParentClasses = append(c.ParentClasses, pc)
	}

	nGroups, err := r.u32Value()
	if err != nil {
		return c, err
	}
	c.GroupDenyMasks = make([]uint64, nGroups)
	c.GroupExemptMasks = make([]uint64, nGroups)
	c.GroupOrigDenied = make([]bool, nGroups)
	for i := range c.GroupDenyMasks {
		if c.GroupDenyMasks[i], err = r.u64Value(); err != nil {
			return c, err
		}
	}
	for i := range c.GroupExemptMasks {
		if c.GroupExemptMasks[i], err = r.u64Value(); err != nil {
			return c, err
		}
	}
	for i := range c.GroupOrigDenied {
		if c.GroupOrigDenied[i], err = readBool(r); err != nil {
			return c, err
		}
	}
	if r.pos != len(r.buf) {
		return c, ErrPackage
	}
	return c, nil
}
