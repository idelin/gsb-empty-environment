package submigrate

import (
	"crypto/sha256"
	"reflect"
	"testing"
)

// goldenHistory is the full small reference history. Hidden slots are
// interspersed so every assertion is proven independent of hidden layout.
type goldenHistory struct {
	host    *Host
	service *Service

	ids       map[string]EventID
	summaries map[string]Digest

	roots   map[PartitionID]*PartitionRoot
	entries map[PartitionID][]LogEntry
	visible map[PartitionID][]*VisibleEvent

	merged []*VisibleEvent
}

func digestOf(s string) Digest { return sha256.Sum256([]byte(s)) }

func buildGolden(t *testing.T) *goldenHistory {
	t.Helper()
	host := NewHost([]byte("golden-host"))
	service := NewService([]byte("golden-service"))

	ids := map[string]EventID{
		"a":  EventID("event-a"),
		"b":  EventID("event-b"),
		"c":  EventID("event-c"),
		"d":  EventID("event-d"),
		"e":  EventID("event-e"),
		"f":  EventID("event-f"),
		"c2": EventID("event-c-prime"), // different identity, identical summary to c
	}
	summaries := map[string]Digest{
		"a":  digestOf("payload-a"),
		"b":  digestOf("payload-b"),
		"c":  digestOf("payload-c"),
		"c2": digestOf("payload-c"),
		"d":  digestOf("payload-d"),
		"e":  digestOf("payload-e"),
		"f":  digestOf("payload-f"),
	}

	ev := func(key string) *VisibleEvent {
		return host.Visible(ids[key], summaries[key])
	}

	// Partition P1 visible order: a, b, c, d (hidden slots interleaved).
	p1 := []LogEntry{
		{Visible: ev("a")},
		host.Hidden([]byte("p1-hidden-1")),
		{Visible: ev("b")},
		{Visible: ev("c")},
		host.Hidden([]byte("p1-hidden-2")),
		{Visible: ev("d")},
	}
	// Partition P2 visible order: b, e, c', f (c' shares c's summary).
	p2 := []LogEntry{
		host.Hidden([]byte("p2-hidden-1")),
		{Visible: ev("b")},
		{Visible: ev("e")},
		{Visible: ev("c2")},
		host.Hidden([]byte("p2-hidden-2")),
		{Visible: ev("f")},
	}

	rootP1, visP1 := host.BuildPartition("P1", 1, p1)
	rootP2, visP2 := host.BuildPartition("P2", 1, p2)

	// Host partial order, interleaved while preserving per-partition order.
	merged := host.Merge(
		[]*VisibleEvent{visP1[0], visP1[1], visP2[0]}, // a, b, b
		[]*VisibleEvent{visP2[1], visP1[2]},           // e, c
		[]*VisibleEvent{visP2[2], visP1[3], visP2[3]}, // c', d, f
	)

	return &goldenHistory{
		host:      host,
		service:   service,
		ids:       ids,
		summaries: summaries,
		roots: map[PartitionID]*PartitionRoot{
			"P1": rootP1,
			"P2": rootP2,
		},
		entries: map[PartitionID][]LogEntry{"P1": p1, "P2": p2},
		visible: map[PartitionID][]*VisibleEvent{"P1": visP1, "P2": visP2},
		merged:  merged,
	}
}

func (g *goldenHistory) rootList() []*PartitionRoot {
	return []*PartitionRoot{g.roots["P1"], g.roots["P2"]}
}

const readerAlice = ReaderID("reader-alice")

func fullReceipts(host *Host, reader ReaderID) []*Receipt {
	// Full old authorization: every visible event is in scope and confirmed.
	return []*Receipt{
		host.IssueReceipt(reader, "P1", 1, 4, 4),
		host.IssueReceipt(reader, "P2", 1, 4, 4),
	}
}

func TestCrossPartitionDedup(t *testing.T) {
	g := buildGolden(t)
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})
	if res.Status != StatusResumable {
		t.Fatalf("status = %s (%s), want resumable", res.Status, res.Reason)
	}

	src := map[string][]Source{
		"a":  {{Partition: "P1", VisSeq: 1}},
		"b":  {{Partition: "P1", VisSeq: 2}, {Partition: "P2", VisSeq: 1}},
		"e":  {{Partition: "P2", VisSeq: 2}},
		"c":  {{Partition: "P1", VisSeq: 3}},
		"c2": {{Partition: "P2", VisSeq: 3}},
		"d":  {{Partition: "P1", VisSeq: 4}},
		"f":  {{Partition: "P2", VisSeq: 4}},
	}
	want := []Notification{
		{EventID: g.ids["a"], Summary: g.summaries["a"], Sources: src["a"]},
		{EventID: g.ids["b"], Summary: g.summaries["b"], Sources: src["b"]},
		{EventID: g.ids["e"], Summary: g.summaries["e"], Sources: src["e"]},
		{EventID: g.ids["c"], Summary: g.summaries["c"], Sources: src["c"]},
		{EventID: g.ids["c2"], Summary: g.summaries["c2"], Sources: src["c2"]},
		{EventID: g.ids["d"], Summary: g.summaries["d"], Sources: src["d"]},
		{EventID: g.ids["f"], Summary: g.summaries["f"], Sources: src["f"]},
	}
	if !reflect.DeepEqual(res.Notifications, want) {
		t.Fatalf("notifications mismatch\n got %#v\nwant %#v", res.Notifications, want)
	}
	if len(res.Diff) != 0 {
		t.Fatalf("diff = %#v, want empty for fully confirmed history", res.Diff)
	}

	// "b" appears in both partitions but collapses to one signed-identity event.
	var bNotif *Notification
	for i := range res.Notifications {
		if res.Notifications[i].EventID == g.ids["b"] {
			bNotif = &res.Notifications[i]
		}
	}
	if bNotif == nil || len(bNotif.Sources) != 2 {
		t.Fatalf("cross-partition duplicate not deduplicated: %#v", bNotif)
	}
	if !NewCursorVerifier().VerifyCursor(res.Cursor, g.rootList()) {
		t.Fatal("cursor failed independent verification")
	}
}

func TestSameSummaryDifferentIdentity(t *testing.T) {
	g := buildGolden(t)
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: fullReceipts(g.host, readerAlice),
		Stream:   g.merged,
	})
	if res.Status != StatusResumable {
		t.Fatalf("status = %s, want resumable", res.Status)
	}
	count := 0
	for _, n := range res.Notifications {
		if n.Summary == g.summaries["c"] {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("same-summary/distinct-identity events collapsed: count=%d", count)
	}
}

func TestOneSideVisible(t *testing.T) {
	g := buildGolden(t)
	// Only P1 is authorized. Reaching a P2 occurrence in the merged stream is
	// outside the old authorization scope and must force a resync, no cursor.
	res := g.service.Migrate(&MigrateRequest{
		Reader:  readerAlice,
		Roots:   g.rootList(),
		Entries: g.entries,
		Receipts: []*Receipt{
			g.host.IssueReceipt(readerAlice, "P1", 1, 4, 4),
		},
		Stream: g.merged,
	})
	if res.Status != StatusResync || res.Reason != ReasonOccurrenceOutOfScope {
		t.Fatalf("got (%s,%s), want resync/%s", res.Status, res.Reason, ReasonOccurrenceOutOfScope)
	}
	if res.Cursor != nil {
		t.Fatal("out-of-scope migration must not emit a cursor")
	}

	// A P1-only merged stream migrates cleanly and never reveals P2-only events.
	res = g.service.Migrate(&MigrateRequest{
		Reader:  readerAlice,
		Roots:   g.rootList(),
		Entries: g.entries,
		Receipts: []*Receipt{
			g.host.IssueReceipt(readerAlice, "P1", 1, 4, 4),
		},
		Stream: g.host.Merge(g.visible["P1"]),
	})
	if res.Status != StatusResumable {
		t.Fatalf("P1-only status = %s (%s)", res.Status, res.Reason)
	}
	for _, n := range res.Notifications {
		if n.EventID == g.ids["e"] || n.EventID == g.ids["c2"] || n.EventID == g.ids["f"] {
			t.Fatalf("P2-only event leaked into P1 migration: %s", n.EventID)
		}
	}
	if !NewCursorVerifier().VerifyCursor(res.Cursor, g.rootList()) {
		t.Fatal("cursor failed independent verification")
	}
}

func TestRevocationDuringMerge(t *testing.T) {
	g := buildGolden(t)
	res := g.service.Migrate(&MigrateRequest{
		Reader:      readerAlice,
		Roots:       g.rootList(),
		Entries:     g.entries,
		Receipts:    fullReceipts(g.host, readerAlice),
		Revocations: []*Revocation{g.host.Revoke(readerAlice, "P2", 1, 2)},
		Stream:      g.merged,
	})
	if res.Status != StatusRevoked || res.Reason != ReasonRevoked {
		t.Fatalf("got (%s,%s), want revoked", res.Status, res.Reason)
	}
	if res.Cursor != nil || len(res.Notifications) != 0 {
		t.Fatal("revoked migration must emit no cursor and no notifications")
	}

	// A revocation for another reader does not revoke Alice.
	res = g.service.Migrate(&MigrateRequest{
		Reader:      readerAlice,
		Roots:       g.rootList(),
		Entries:     g.entries,
		Receipts:    fullReceipts(g.host, readerAlice),
		Revocations: []*Revocation{g.host.Revoke(ReaderID("reader-bob"), "P2", 1, 2)},
		Stream:      g.merged,
	})
	if res.Status != StatusResumable {
		t.Fatalf("foreign revocation wrongly applied: %s (%s)", res.Status, res.Reason)
	}

	// A forged revocation signature is not an authentic withdrawal: force resync.
	forged := g.host.Revoke(readerAlice, "P2", 1, 2)
	forged.Signature[0] ^= 0xFF
	res = g.service.Migrate(&MigrateRequest{
		Reader:      readerAlice,
		Roots:       g.rootList(),
		Entries:     g.entries,
		Receipts:    fullReceipts(g.host, readerAlice),
		Revocations: []*Revocation{forged},
		Stream:      g.merged,
	})
	if res.Status != StatusResync || res.Reason != ReasonRevocationBadSignature {
		t.Fatalf("forged revocation got (%s,%s)", res.Status, res.Reason)
	}
}

func TestSafeResumeInterleavedSecondHalf(t *testing.T) {
	g := buildGolden(t)
	// Authorized for everything, but only the first two visible events of each
	// partition were confirmed. The safe continuation is exactly the interleaved
	// second half and is reported as diff.
	receipts := []*Receipt{
		g.host.IssueReceipt(readerAlice, "P1", 1, 4, 2),
		g.host.IssueReceipt(readerAlice, "P2", 1, 4, 2),
	}
	res := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: receipts,
		Stream:   g.merged,
	})
	if res.Status != StatusResumable {
		t.Fatalf("status = %s (%s)", res.Status, res.Reason)
	}

	// Confirmed union: P1 {a,b}, P2 {b,e}. New merged identities in order are
	// c, c', d, f.
	wantDiffIDs := []EventID{g.ids["c"], g.ids["c2"], g.ids["d"], g.ids["f"]}
	if len(res.Diff) != len(wantDiffIDs) {
		t.Fatalf("diff len = %d, want %d (%#v)", len(res.Diff), len(wantDiffIDs), res.Diff)
	}
	for i, id := range wantDiffIDs {
		if res.Diff[i].EventID != id {
			t.Fatalf("diff[%d] = %s, want %s", i, res.Diff[i].EventID, id)
		}
	}

	// Replay with identical inputs is byte-stable: always safe to resume.
	res2 := g.service.Migrate(&MigrateRequest{
		Reader:   readerAlice,
		Roots:    g.rootList(),
		Entries:  g.entries,
		Receipts: receipts,
		Stream:   g.merged,
	})
	if !reflect.DeepEqual(res.Cursor, res2.Cursor) ||
		!reflect.DeepEqual(res.Notifications, res2.Notifications) ||
		!reflect.DeepEqual(res.Diff, res2.Diff) {
		t.Fatal("migration is not deterministic across replays")
	}
	if !NewCursorVerifier().VerifyCursor(res.Cursor, g.rootList()) {
		t.Fatal("cursor failed independent verification")
	}
}
