package snapshotscan

import (
	"errors"
	"fmt"
	"testing"
)

const subject = "alice"

// seedStore loads keys k01..kNN (zero-padded) with value "v-<key>".
func seedStore(t *testing.T, n int) *Store {
	t.Helper()
	s := NewStore()
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("k%02d", i)
		s.Put(key, "v-"+key)
	}
	return s
}

func grantRange(p *PermissionModel, from, to int) {
	for i := from; i <= to; i++ {
		p.Grant(subject, fmt.Sprintf("k%02d", i))
	}
}

func keysOf(items []Item) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return keys
}

func equalKeys(a, b []string) bool {
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

// collect drains the scan, asserting per-page permission expectations
// via betweenPages (invoked after each page, before fetching the next).
func collect(t *testing.T, sc *Scanner, limit int, betweenPages func(page int)) []Item {
	t.Helper()
	var all []Item
	cursor := ""
	for page := 0; ; page++ {
		p, err := sc.Scan(subject, cursor, limit)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		all = append(all, p.Items...)
		if betweenPages != nil {
			betweenPages(page)
		}
		if !p.HasMore {
			return all
		}
		cursor = p.NextCursor
	}
}

// Data snapshot is pinned at scan start: later inserts, deletes and
// updates are invisible to the ongoing scan.
func TestSnapshotPinnedAtScanStart(t *testing.T) {
	store := seedStore(t, 4)
	perms := NewPermissionModel()
	grantRange(perms, 1, 10)
	sc := NewScanner(store, perms, 2)

	page1, err := sc.Scan(subject, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}

	// Mutate live data after the snapshot was pinned.
	store.Put("k00", "v-new")     // insert before cursor
	store.Put("k03", "v-changed") // update not-yet-scanned key
	store.Delete("k04")           // delete not-yet-scanned key
	store.Put("k05", "v-new")     // insert after cursor

	page2, err := sc.Scan(subject, page1.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page2.HasMore {
		t.Fatal("expected terminal page")
	}
	// Snapshot view: k03 keeps its old value, k04 still present, k00/k05 absent.
	want := []Item{{"k03", "v-k03"}, {"k04", "v-k04"}}
	if len(page2.Items) != len(want) {
		t.Fatalf("page2 = %v", page2.Items)
	}
	for i, it := range page2.Items {
		if it != want[i] {
			t.Fatalf("page2[%d] = %+v, want %+v", i, it, want[i])
		}
	}
}

// Revocation after the snapshot but before delivery hides the item:
// skipped items never leak into any page.
func TestRevokedBeforeDeliveryNotLeaked(t *testing.T) {
	store := seedStore(t, 6)
	perms := NewPermissionModel()
	grantRange(perms, 1, 6)
	sc := NewScanner(store, perms, 3)

	page1, err := sc.Scan(subject, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}

	// Revoke k03/k04 after the scan started, before they are delivered.
	perms.Revoke(subject, "k03")
	perms.Revoke(subject, "k04")

	rest := []Item{}
	cursor := page1.NextCursor
	for {
		p, err := sc.Scan(subject, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, p.Items...)
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}
	if got := keysOf(rest); !equalKeys(got, []string{"k05", "k06"}) {
		t.Fatalf("rest = %v, want [k05 k06] (k03/k04 must be skipped)", got)
	}
}

// The mirror image: a grant issued after the snapshot but before the
// item's delivery opportunity takes effect, because permission is
// evaluated at delivery time against the latest model version.
func TestGrantedAfterSnapshotIsDelivered(t *testing.T) {
	store := seedStore(t, 3)
	perms := NewPermissionModel()
	perms.Grant(subject, "k01")
	perms.Grant(subject, "k03")
	sc := NewScanner(store, perms, 2)

	page1, err := sc.Scan(subject, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01"}) {
		t.Fatalf("page1 = %v", got)
	}

	perms.Grant(subject, "k02") // dynamic grant, post-snapshot

	got := collectFrom(t, sc, page1.NextCursor, 10)
	if !equalKeys(got, []string{"k02", "k03"}) {
		t.Fatalf("rest = %v, want [k02 k03]", got)
	}
}

// An item already delivered is never retracted: revoking it afterwards
// does not remove it from the page the client already received, and the
// scan simply moves on.
func TestDeliveredItemsNotRetracted(t *testing.T) {
	store := seedStore(t, 4)
	perms := NewPermissionModel()
	grantRange(perms, 1, 4)
	sc := NewScanner(store, perms, 2)

	page1, err := sc.Scan(subject, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}

	// Revoke an already-delivered key; the delivered page is history.
	perms.Revoke(subject, "k01")
	perms.Revoke(subject, "k02")

	got := collectFrom(t, sc, page1.NextCursor, 2)
	if !equalKeys(got, []string{"k03", "k04"}) {
		t.Fatalf("rest = %v, want [k03 k04]", got)
	}
	// page1 contents are untouched by the revocation.
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01", "k02"}) {
		t.Fatalf("delivered page mutated: %v", got)
	}
}

// The cursor records the last SCANNED key, not the last delivered key.
// When the scan tail consists of skipped (revoked) items, the terminal
// cursor still points past them.
func TestCursorTracksLastScannedNotLastDelivered(t *testing.T) {
	store := seedStore(t, 5)
	perms := NewPermissionModel()
	grantRange(perms, 1, 3) // k04, k05 never granted -> skipped at scan tail
	sc := NewScanner(store, perms, 2)

	page, err := sc.Scan(subject, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore {
		t.Fatal("expected terminal page")
	}
	if got := keysOf(page.Items); !equalKeys(got, []string{"k01", "k02", "k03"}) {
		t.Fatalf("items = %v", got)
	}
	cur, err := DecodeCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if cur.LastScanned != "k05" {
		t.Fatalf("cursor.LastScanned = %q, want k05 (last scanned, not last delivered k03)", cur.LastScanned)
	}
	if cur.Snapshot != store.Pin() {
		t.Fatalf("cursor.Snapshot = %d, want pinned %d", cur.Snapshot, store.Pin())
	}
}

// A skipped item is never revisited: revoking then re-granting between
// pages does not resurrect it, because the scan position has moved on.
func TestSkippedItemNotRevisitedAfterRegrant(t *testing.T) {
	store := seedStore(t, 4)
	perms := NewPermissionModel()
	perms.Grant(subject, "k01")
	perms.Grant(subject, "k02")
	perms.Grant(subject, "k04")
	sc := NewScanner(store, perms, 4) // batch covers k01..k04 at once

	page1, err := sc.Scan(subject, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	// k03 is scanned and skipped inside page1's assembly.
	if got := keysOf(page1.Items); !equalKeys(got, []string{"k01", "k02", "k04"}) {
		t.Fatalf("page1 = %v", got)
	}
	cur, _ := DecodeCursor(page1.NextCursor)
	if cur.LastScanned != "k04" {
		t.Fatalf("cursor.LastScanned = %q, want k04", cur.LastScanned)
	}

	perms.Grant(subject, "k03") // too late: its delivery opportunity passed

	got := collectFrom(t, sc, page1.NextCursor, 3)
	if len(got) != 0 {
		t.Fatalf("k03 leaked after re-grant: %v", got)
	}
}

// Full-pagination audit under a dynamic permission model: permissions
// change between pages, and the final delivered stream must contain
// exactly the items permitted at their delivery opportunity, each once,
// in key order, with no duplicates and no leaks.
func TestPaginationAuditWithDynamicPermissions(t *testing.T) {
	store := seedStore(t, 9)
	perms := NewPermissionModel()
	grantRange(perms, 1, 9)
	sc := NewScanner(store, perms, 2) // small internal batches on purpose

	var delivered []Item
	cursor := ""
	for page := 0; ; page++ {
		switch page {
		case 1:
			perms.Revoke(subject, "k04") // will be skipped
			perms.Revoke(subject, "k05") // will be skipped
		case 2:
			perms.Grant(subject, "k04")  // re-grant, but k04 already scanned
			perms.Revoke(subject, "k09") // revoked before its delivery
		}
		p, err := sc.Scan(subject, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			if !perms.Allowed(subject, it.Key) {
				t.Fatalf("delivered %s while not permitted", it.Key)
			}
		}
		delivered = append(delivered, p.Items...)
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}

	want := []string{"k01", "k02", "k03", "k06", "k07", "k08"}
	if got := keysOf(delivered); !equalKeys(got, want) {
		t.Fatalf("delivered = %v, want %v", got, want)
	}
	// No duplicates.
	seen := map[string]bool{}
	for _, it := range delivered {
		if seen[it.Key] {
			t.Fatalf("duplicate delivery of %s", it.Key)
		}
		seen[it.Key] = true
	}
	// Skipped/revoked items never appear.
	for _, leaked := range []string{"k04", "k05", "k09"} {
		if seen[leaked] {
			t.Fatalf("%s leaked into delivery stream", leaked)
		}
	}
}

// The cursor pins the snapshot version: resuming after further commits
// still reads the original snapshot, and a pruned snapshot fails loudly.
func TestCursorPinsSnapshotAndExpiry(t *testing.T) {
	store := seedStore(t, 4)
	perms := NewPermissionModel()
	grantRange(perms, 1, 4)
	sc := NewScanner(store, perms, 2)

	page1, err := sc.Scan(subject, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := DecodeCursor(page1.NextCursor)
	pinned := cur.Snapshot

	for i := 5; i <= 8; i++ { // advance the live version far beyond the pin
		store.Put(fmt.Sprintf("k%02d", i), "v-new")
	}
	page2, err := sc.Scan(subject, page1.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page2.Items); !equalKeys(got, []string{"k03", "k04"}) {
		t.Fatalf("page2 = %v, want snapshot view [k03 k04]", got)
	}

	store.PruneBefore(pinned + 2) // prune the pinned snapshot
	if _, err := sc.Scan(subject, page1.NextCursor, 10); !errors.Is(err, ErrSnapshotExpired) {
		t.Fatalf("err = %v, want ErrSnapshotExpired", err)
	}
}

// Static permissions: every permitted item is delivered exactly once,
// in order, across arbitrary page and batch sizes.
func TestPaginationCompleteAndOrdered(t *testing.T) {
	for _, limit := range []int{1, 3, 4, 100} {
		for _, batch := range []int{1, 2, 7} {
			store := seedStore(t, 10)
			perms := NewPermissionModel()
			grantRange(perms, 1, 10)
			perms.Revoke(subject, "k03")
			perms.Revoke(subject, "k07")
			sc := NewScanner(store, perms, batch)

			got := collect(t, sc, limit, nil)
			want := []string{"k01", "k02", "k04", "k05", "k06", "k08", "k09", "k10"}
			if !equalKeys(keysOf(got), want) {
				t.Fatalf("limit=%d batch=%d: got %v, want %v", limit, batch, keysOf(got), want)
			}
		}
	}
}

func TestCursorRoundTripAndValidation(t *testing.T) {
	cur := Cursor{Snapshot: 42, LastScanned: "k07"}
	decoded, err := DecodeCursor(EncodeCursor(cur))
	if err != nil {
		t.Fatal(err)
	}
	if decoded != cur {
		t.Fatalf("round trip = %+v, want %+v", decoded, cur)
	}
	if _, err := DecodeCursor("not-a-cursor"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("err = %v, want ErrInvalidCursor", err)
	}
	if _, err := NewScanner(seedStore(t, 1), NewPermissionModel(), 1).Scan(subject, "not-a-cursor", 1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("err = %v, want ErrInvalidCursor", err)
	}
}

func TestInvalidLimit(t *testing.T) {
	sc := NewScanner(seedStore(t, 1), NewPermissionModel(), 1)
	if _, err := sc.Scan(subject, "", 0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("err = %v, want ErrInvalidLimit", err)
	}
}

func collectFrom(t *testing.T, sc *Scanner, cursor string, limit int) []string {
	t.Helper()
	var got []string
	for {
		p, err := sc.Scan(subject, cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, keysOf(p.Items)...)
		if !p.HasMore {
			return got
		}
		cursor = p.NextCursor
	}
}
