package bootpolicy

// VerifyCertificate independently establishes that compressed admits exactly
// the same load events as original over the finite partition described by
// cert. It does not trust any decision mask carried by the certificate:
// partitions are rebuilt from the entry sets and every finite class is
// enumerated and re-evaluated.
func VerifyCertificate(original, compressed *Policy, cert Certificate) error {
	if err := original.Validate(); err != nil {
		return err
	}
	if err := compressed.Validate(); err != nil {
		return err
	}
	if cert.OriginalEntryCount != len(original.Entries) {
		return ErrCertificate
	}

	// 1. No new recovery exemptions: every compressed exemption must already
	// exist, byte-for-byte, in the original policy.
	for i := range compressed.Entries {
		e := compressed.Entries[i]
		if e.Kind == KindExempt && !containsEntry(original.Entries, e) {
			return ErrNewExemption
		}
	}

	// 2. Structural sanity of the claimed class boundaries.
	if err := sanityCheckClasses(cert); err != nil {
		return err
	}

	// 3. Rebuild the partition over exactly the original and the installed
	// compressed entries; the certificate claims equivalence over this union.
	union := make([]Entry, 0, len(original.Entries)+len(compressed.Entries))
	union = append(union, original.Entries...)
	union = append(union, compressed.Entries...)
	part, err := BuildPartition(union, len(original.Entries), verifyCellLimit)
	if err != nil {
		return err
	}
	rebuilt := part.Certificate()
	if err := compareCertificates(cert, rebuilt); err != nil {
		return err
	}

	// 4. Explicit per-class decision check using the actual evaluator, so the
	// mask bookkeeping used by Compress is cross-checked end to end.
	selectedMask := uint64(0)
	for ci := range compressed.Entries {
		unionIdx := len(original.Entries) + ci
		selectedMask |= 1 << uint(unionIdx)
	}
	for _, g := range part.groups {
		ev := g.event
		origAdmit := original.EventAdmit(ev)
		compAdmit := compressed.EventAdmit(ev)
		if origAdmit != compAdmit {
			return ErrCertificate
		}
		selectedDeny := g.denyMask & selectedMask
		selectedExempt := g.exemptMask & selectedMask
		if decideDenied(selectedDeny, selectedExempt) == origAdmit {
			return ErrCertificate
		}
		wantDenied := !origAdmit
		if wantDenied != g.origDenied {
			return ErrCertificate
		}
	}

	// 5. The certificate's own group masks must agree with the rebuilt ones.
	if len(cert.GroupDenyMasks) != len(rebuilt.GroupDenyMasks) {
		return ErrCertificate
	}
	for i := range rebuilt.GroupDenyMasks {
		if cert.GroupDenyMasks[i] != rebuilt.GroupDenyMasks[i] ||
			cert.GroupExemptMasks[i] != rebuilt.GroupExemptMasks[i] ||
			cert.GroupOrigDenied[i] != rebuilt.GroupOrigDenied[i] {
			return ErrCertificate
		}
	}
	return nil
}

// verifyCellLimit is deliberately larger than MaxPartitionCells so a package
// built at the compression limit can always be independently re-checked.
const verifyCellLimit = 16_000_000

func sanityCheckClasses(cert Certificate) error {
	if len(cert.PrefixClasses) == 0 || len(cert.GenClasses) == 0 ||
		len(cert.LocationClasses) == 0 || len(cert.ParentClasses) == 0 {
		return ErrCertificate
	}
	hasExempt := len(cert.DigestClasses) > 0 || len(cert.TimeClasses) > 0 || len(cert.RootClasses) > 0
	if hasExempt && (len(cert.DigestClasses) == 0 || len(cert.TimeClasses) == 0 || len(cert.RootClasses) == 0) {
		return ErrCertificate
	}
	for i := range cert.GenClasses {
		gc := &cert.GenClasses[i]
		if gc.Lo > gc.Hi || gc.Representative < gc.Lo || gc.Representative > gc.Hi {
			return ErrCertificate
		}
		if i > 0 && gc.Lo <= cert.GenClasses[i-1].Hi {
			return ErrCertificate
		}
	}
	for i := range cert.TimeClasses {
		tc := &cert.TimeClasses[i]
		if tc.Lo > tc.Hi || tc.Representative < tc.Lo || tc.Representative > tc.Hi {
			return ErrCertificate
		}
	}
	for _, pc := range cert.PrefixClasses {
		if !hasPrefix(pc.Representative, pc.Prefix) {
			return ErrCertificate
		}
		if pc.Representative == "" && pc.Prefix != "" {
			return ErrCertificate
		}
	}
	return nil
}

func compareCertificates(a, b Certificate) error {
	if a.OriginalEntryCount != b.OriginalEntryCount ||
		len(a.Prefixes) != len(b.Prefixes) ||
		len(a.PrefixClasses) != len(b.PrefixClasses) ||
		len(a.GenClasses) != len(b.GenClasses) ||
		len(a.LocationClasses) != len(b.LocationClasses) ||
		len(a.DigestClasses) != len(b.DigestClasses) ||
		len(a.RootClasses) != len(b.RootClasses) ||
		len(a.TimeClasses) != len(b.TimeClasses) ||
		len(a.ParentClasses) != len(b.ParentClasses) {
		return ErrCertificate
	}
	for i := range a.Prefixes {
		if a.Prefixes[i] != b.Prefixes[i] {
			return ErrCertificate
		}
	}
	for i := range a.GenClasses {
		if a.GenClasses[i] != b.GenClasses[i] {
			return ErrCertificate
		}
	}
	for i := range a.TimeClasses {
		if a.TimeClasses[i] != b.TimeClasses[i] {
			return ErrCertificate
		}
	}
	for i := range a.PrefixClasses {
		x, y := a.PrefixClasses[i], b.PrefixClasses[i]
		if x.Representative != y.Representative || x.IsNode != y.IsNode ||
			x.Prefix != y.Prefix || !intsEqual(x.MatchedPrefixes, y.MatchedPrefixes) {
			return ErrCertificate
		}
	}
	for i := range a.LocationClasses {
		if !valueClassEqual(a.LocationClasses[i], b.LocationClasses[i]) {
			return ErrCertificate
		}
	}
	if len(a.RootClasses) != len(b.RootClasses) {
		return ErrCertificate
	}
	for i := range a.RootClasses {
		if !valueClassEqual(a.RootClasses[i], b.RootClasses[i]) {
			return ErrCertificate
		}
	}
	for i := range a.DigestClasses {
		x, y := a.DigestClasses[i], b.DigestClasses[i]
		if x.Wildcard != y.Wildcard || !x.Representative.equal(y.Representative) ||
			!intsEqual(x.MatchedIndexes, y.MatchedIndexes) {
			return ErrCertificate
		}
	}
	for i := range a.ParentClasses {
		x, y := a.ParentClasses[i], b.ParentClasses[i]
		if x.NilParent != y.NilParent || x.Wildcard != y.Wildcard ||
			!x.Scope.equal(y.Scope) ||
			!intsEqual(x.MatchedIndexes, y.MatchedIndexes) ||
			!intsEqual(x.UnscopedIndexes, y.UnscopedIndexes) {
			return ErrCertificate
		}
	}
	return nil
}

func valueClassEqual(a, b ValueClass) bool {
	return a.Representative == b.Representative && a.Wildcard == b.Wildcard &&
		intsEqual(a.MatchedIndexes, b.MatchedIndexes)
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
