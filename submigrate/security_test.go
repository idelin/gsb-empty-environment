package submigrate

import "testing"

func TestOutputIndependentOfHiddenCount(t *testing.T) {
	g := buildGolden(t)
	base := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})

	// Rebuild the same visible history with different hidden slot counts and
	// positions. Visible events and their visibility sequence numbers are
	// unchanged, so every visible output must be identical.
	ev := func(key string) *VisibleEvent {
		return g.host.Visible(g.ids[key], g.summaries[key])
	}
	p1Alt := []LogEntry{
		hostHidden(t, g.host, "extra-1"),
		{Visible: ev("a")},
		{Visible: ev("b")},
		hostHidden(t, g.host, "extra-2"),
		hostHidden(t, g.host, "extra-3"),
		{Visible: ev("c")},
		{Visible: ev("d")},
		hostHidden(t, g.host, "extra-4"),
	}
	p2Alt := []LogEntry{
		{Visible: ev("b")},
		hostHidden(t, g.host, "extra-5"),
		{Visible: ev("e")},
		{Visible: ev("c2")},
		{Visible: ev("f")},
	}
	rootP1, visP1 := g.host.BuildPartition("P1", 1, p1Alt)
	rootP2, visP2 := g.host.BuildPartition("P2", 1, p2Alt)
	altRoots := []*PartitionRoot{rootP1, rootP2}
	altEntries := map[PartitionID][]LogEntry{"P1": p1Alt, "P2": p2Alt}
	altMerged := g.host.Merge(
		[]*VisibleEvent{visP1[0], visP1[1], visP2[0]},
		[]*VisibleEvent{visP2[1], visP1[2]},
		[]*VisibleEvent{visP2[2], visP1[3], visP2[3]},
	)

	alt := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    altRoots,
		Entries:  altEntries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   altMerged,
	})
	if alt.Status != StatusResumable {
		t.Fatalf("hidden-layout variant status = %s (%s)", alt.Status, alt.Reason)
	}
	if !equalNotifications(base.Notifications, alt.Notifications) {
		t.Fatalf("visible notifications depend on hidden layout\nbase %#v\n alt %#v",
			base.Notifications, alt.Notifications)
	}
	if !equalNotifications(base.Diff, alt.Diff) {
		t.Fatalf("diff depends on hidden layout\nbase %#v\n alt %#v", base.Diff, alt.Diff)
	}

	// Progress carries only visible coordinates; physical log length differs.
	if g.roots["P1"].LogLength == altRoots[0].LogLength {
		t.Fatal("test setup failed: hidden counts should differ")
	}
	for i := range base.Cursor.Progress {
		bp, ap := base.Cursor.Progress[i], alt.Cursor.Progress[i]
		if bp != ap {
			t.Fatalf("cursor progress depends on hidden layout: %#v != %#v", bp, ap)
		}
	}
	if !NewCursorVerifier().VerifyCursor(alt.Cursor, altRoots) {
		t.Fatal("hidden-layout cursor failed independent verification")
	}
}

func hostHidden(t *testing.T, h *Host, marker string) LogEntry {
	t.Helper()
	return h.Hidden([]byte(marker))
}

func equalNotifications(a, b []Notification) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].EventID != b[i].EventID || a[i].Summary != b[i].Summary ||
			len(a[i].Sources) != len(b[i].Sources) {
			return false
		}
		for j := range a[i].Sources {
			if a[i].Sources[j] != b[i].Sources[j] {
				return false
			}
		}
	}
	return true
}

func TestRootLogMismatchForcesResync(t *testing.T) {
	g := buildGolden(t)
	// Hand the service a history whose hidden blobs do not match the signed
	// root. The physical-log commitment must reject it before any migration.
	entries := map[PartitionID][]LogEntry{}
	for p, list := range g.entries {
		cloned := cloneLogEntries(list)
		for i := range cloned {
			if cloned[i].Visible == nil {
				cloned[i].HiddenBlob = append([]byte(nil), cloned[i].HiddenBlob...)
				cloned[i].HiddenBlob[0] ^= 0xFF
				break
			}
		}
		entries[p] = cloned
	}
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})
	if res.Status != StatusResync || res.Reason != ReasonRootLogMismatch {
		t.Fatalf("got (%s,%s), want resync/%s", res.Status, res.Reason, ReasonRootLogMismatch)
	}
}

func cloneLogEntries(in []LogEntry) []LogEntry {
	out := make([]LogEntry, len(in))
	copy(out, in)
	return out
}

func TestForeignReaderReceiptRejected(t *testing.T) {
	g := buildGolden(t)
	// Bob's confirmations must never be importable into Alice's migration.
	bobReceipts := fullReceipts(g.host, ReaderID("reader-bob"))
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: bobReceipts,
		Stream:   g.merged,
	})
	if res.Status != StatusResync || res.Reason != ReasonReceiptForeignReader {
		t.Fatalf("foreign receipt got (%s,%s)", res.Status, res.Reason)
	}

	// A verifier also rejects a cursor whose embedded receipt belongs elsewhere.
	good := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})
	if good.Status != StatusResumable {
		t.Fatalf("baseline status = %s", good.Status)
	}
	tampered := *good.Cursor
	tampered.Receipts = append([]*Receipt(nil), good.Cursor.Receipts...)
	tampered.Receipts[0] = bobReceipts[0]
	if NewCursorVerifier().VerifyCursor(&tampered, g.rootList()) {
		t.Fatal("verifier accepted cursor carrying another reader's receipt")
	}
}

func TestMalformedAndTamperedInputsForceResync(t *testing.T) {
	g := buildGolden(t)

	t.Run("forged receipt signature", func(t *testing.T) {
		receipts := fullReceipts(g.host, readerAlice)
		receipts[0].Signature[0] ^= 0xFF
		res := g.service.Migrate(&MigrateRequest{
			Reader: readerAlice, Roots: g.rootList(), Entries: g.entries,
			Receipts: receipts, Stream: g.merged,
		})
		if res.Status != StatusResync || res.Reason != ReasonReceiptBadSignature {
			t.Fatalf("got (%s,%s)", res.Status, res.Reason)
		}
	})

	t.Run("receipt exceeds root visible length", func(t *testing.T) {
		receipts := []*Receipt{
			g.host.IssueReceipt(readerAlice, "P1", 1, 99, 99),
			g.host.IssueReceipt(readerAlice, "P2", 1, 4, 4),
		}
		res := g.service.Migrate(&MigrateRequest{
			Reader: readerAlice, Roots: g.rootList(), Entries: g.entries,
			Receipts: receipts, Stream: g.merged,
		})
		if res.Status != StatusResync || res.Reason != ReasonReceiptScopeOutOfRoot {
			t.Fatalf("got (%s,%s)", res.Status, res.Reason)
		}
	})

	t.Run("visible chain gap", func(t *testing.T) {
		// Drop an occurrence from the merged stream: per-partition VisSeq jumps.
		gapped := append([]*VisibleEvent(nil), g.merged...)
		gapped = append(gapped[:2], gapped[3:]...)
		res := g.service.Migrate(&MigrateRequest{
			Reader: readerAlice, Roots: g.rootList(), Entries: g.entries,
			Receipts: fullReceipts(g.host, readerAlice), Stream: gapped,
		})
		if res.Status != StatusResync || res.Reason != ReasonOccurrenceChainGap {
			t.Fatalf("got (%s,%s)", res.Status, res.Reason)
		}
	})

	t.Run("forged occurrence signature", func(t *testing.T) {
		stream := cloneVisible(g.merged)
		stream[0].OccurrenceSig[0] ^= 0xFF
		res := g.service.Migrate(&MigrateRequest{
			Reader: readerAlice, Roots: g.rootList(), Entries: g.entries,
			Receipts: fullReceipts(g.host, readerAlice), Stream: stream,
		})
		if res.Status != StatusResync || res.Reason != ReasonOccurrenceBadSignature {
			t.Fatalf("got (%s,%s)", res.Status, res.Reason)
		}
	})

	t.Run("identity reused with different content", func(t *testing.T) {
		// Re-sign a valid identity but attach a different summary under the same
		// EventID inside a fresh stream event.
		rogue := g.host.Visible(g.ids["a"], digestOf("different-payload"))
		rogue.Partition = "P1"
		rogue.VisSeq = 1
		rogue.OccurrencePayload = occurrencePayload("P1", 1, rogue.EventID, rogue.Summary)
		rogue.OccurrenceSig = g.host.sign(rogue.OccurrencePayload)
		res := g.service.Migrate(&MigrateRequest{
			Reader: readerAlice, Roots: g.rootList(), Entries: g.entries,
			Receipts: fullReceipts(g.host, readerAlice), Stream: []*VisibleEvent{rogue},
		})
		// The rogue occurrence is not part of the signed P1 visible chain, so the
		// root commitment rejects it rather than silently accepting the swap.
		if res.Status != StatusResync {
			t.Fatalf("identity/content swap got status %s", res.Status)
		}
	})
}

func cloneVisible(in []*VisibleEvent) []*VisibleEvent {
	out := make([]*VisibleEvent, len(in))
	for i, ev := range in {
		cp := *ev
		cp.OccurrenceSig = append([]byte(nil), ev.OccurrenceSig...)
		cp.IdentitySig = append([]byte(nil), ev.IdentitySig...)
		cp.OccurrencePayload = append([]byte(nil), ev.OccurrencePayload...)
		cp.IdentityPayload = append([]byte(nil), ev.IdentityPayload...)
		out[i] = &cp
	}
	return out
}

func TestCursorBindingTamperDetection(t *testing.T) {
	t.Run("no physical sequence leaks", func(t *testing.T) {
		g := buildGolden(t)
		res := g.service.Migrate(&MigrateRequest{
			Reader:   readerAlice,
			Roots:    g.rootList(),
			Entries:  g.entries,
			Receipts: fullReceipts(g.host, readerAlice),
			Stream:   g.merged,
		})
		if res.Status != StatusResumable {
			t.Fatalf("status = %s", res.Status)
		}
		// P1's second visible event sits at physical slot index 3 because a
		// hidden entry precedes it. Its migrated coordinate must be VisSeq 2.
		for _, n := range res.Notifications {
			for _, src := range n.Sources {
				if src.Partition == "P1" && src.VisSeq >= g.roots["P1"].LogLength {
					t.Fatalf("physical slot index leaked into visible source: %#v", src)
				}
			}
		}
		for _, pgr := range res.Cursor.Progress {
			if pgr.ConfirmedVisibleHead > g.roots[pgr.Partition].VisibleLength {
				t.Fatalf("progress exceeds visible length (physical leak): %#v", pgr)
			}
			if pgr.MergedVisSeq != pgr.ConfirmedVisibleHead {
				t.Fatalf("cursor carries an independent physical coordinate: %#v", pgr)
			}
		}
	})

	g := buildGolden(t)
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})
	if res.Status != StatusResumable {
		t.Fatalf("status = %s", res.Status)
	}
	verifier := NewCursorVerifier()
	if !verifier.VerifyCursor(res.Cursor, g.rootList()) {
		t.Fatal("valid cursor rejected")
	}

	t.Run("service signature tampered", func(t *testing.T) {
		cp := *res.Cursor
		cp.ServiceSig = append([]byte(nil), res.Cursor.ServiceSig...)
		cp.ServiceSig[0] ^= 0xFF
		if verifier.VerifyCursor(&cp, g.rootList()) {
			t.Fatal("tampered service signature accepted")
		}
	})

	t.Run("reader rebound", func(t *testing.T) {
		cp := *res.Cursor
		cp.Reader = ReaderID("reader-bob")
		if verifier.VerifyCursor(&cp, g.rootList()) {
			t.Fatal("cursor rebound to another reader accepted")
		}
	})

	t.Run("evidence removed", func(t *testing.T) {
		cp := *res.Cursor
		cp.Evidence = res.Cursor.Evidence[1:]
		if verifier.VerifyCursor(&cp, g.rootList()) {
			t.Fatal("cursor with removed evidence accepted")
		}
	})

	t.Run("wrong roots set", func(t *testing.T) {
		other := buildGolden(t)
		otherHost := NewHost([]byte("different-host"))
		root, _ := otherHost.BuildPartition("P1", 1, other.entries["P1"])
		if verifier.VerifyCursor(res.Cursor, []*PartitionRoot{root, g.roots["P2"]}) {
			t.Fatal("cursor accepted against foreign roots")
		}
	})
}
