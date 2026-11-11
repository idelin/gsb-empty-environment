package netsec

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, d *Dictionary, word string, origin Origin) ID {
	t.Helper()
	id, err := d.Add([]byte(word), origin)
	if err != nil {
		t.Fatalf("Add(%q): %v", word, err)
	}
	return id
}

func mustDerive(t *testing.T, d *Dictionary, word string, parents ...ID) ID {
	t.Helper()
	id, err := d.Derive([]byte(word), parents...)
	if err != nil {
		t.Fatalf("Derive(%q): %v", word, err)
	}
	return id
}

func originOf(tenant string, digestInput string, public bool) Origin {
	return Origin{
		Tenant: Tenant(tenant),
		Input:  SumDigest([]byte(digestInput)),
		Public: public,
	}
}

func TestAddValidationAndSnapshotIntersection(t *testing.T) {
	d := New()
	secretA := originOf("tenant-a", "input-a-secret", false)

	if _, err := d.Add(nil, secretA); !errors.Is(err, ErrEmptyWord) {
		t.Fatalf("empty word: got %v want ErrEmptyWord", err)
	}
	if _, err := d.Add([]byte("tok"), Origin{Tenant: "x"}); !errors.Is(err, ErrZeroDigest) {
		t.Fatalf("zero digest: got %v want ErrZeroDigest", err)
	}
	if _, err := d.Add([]byte("tok"), Origin{Input: SumDigest([]byte("i"))}); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant: got %v want ErrEmptyTenant", err)
	}

	idA := mustAdd(t, d, "token", secretA)

	snapA, err := d.Snapshot([]Tenant{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Len() != 1 {
		t.Fatalf("tenant-a snapshot len = %d, want 1", snapA.Len())
	}
	snapB, err := d.Snapshot([]Tenant{"tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	if snapB.Len() != 0 {
		t.Fatalf("tenant-b snapshot len = %d, want 0: secret leaked across tenants", snapB.Len())
	}

	idPub := mustAdd(t, d, "GET", originOf("tenant-a", "spec-input", true))
	snapB2, err := d.Snapshot([]Tenant{"tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapB2.ByWord([]byte("GET")); !slices.Equal(got, []ID{idPub}) {
		t.Fatalf("public entry not shared: %v", got)
	}

	if _, err := d.Snapshot(nil); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty snapshot auth: %v", err)
	}
	if ok, err := d.Eligible(idA, []Tenant{"tenant-b"}); err != nil || ok {
		t.Fatalf("cross-tenant eligibility = %v, %v; want false, nil", ok, err)
	}
}

func TestSameByteDifferentOriginsKeepSeparateRestrictions(t *testing.T) {
	d := New()
	secretA := originOf("tenant-a", "secret-input", false)
	publicB := originOf("tenant-b", "public-input", true)

	idSecret := mustAdd(t, d, "TOKEN", secretA)
	idPublic := mustAdd(t, d, "TOKEN", publicB)

	if idSecret == idPublic {
		t.Fatal("identical bytes collapsed into one entry")
	}

	got, err := d.Entry(idSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.SecretTenants(), []Tenant{"tenant-a"}) {
		t.Fatalf("secret tenants = %v", got.SecretTenants())
	}

	snapB, err := d.Snapshot([]Tenant{"tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	matches := snapB.ByWord([]byte("TOKEN"))
	if len(matches) != 1 || matches[0] != idPublic {
		t.Fatalf("tenant-b sees %v, want only public entry %d", matches, idPublic)
	}

	snapA, err := d.Snapshot([]Tenant{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapA.ByWord([]byte("TOKEN"))) != 2 {
		t.Fatal("authorized tenant lost one of the same-bytes entries")
	}

	if err := d.Revoke("tenant-a", secretA.Input); err != nil {
		t.Fatal(err)
	}
	dead, err := d.Entry(idSecret)
	if err != nil || !dead.Revoked {
		t.Fatalf("secret entry revoked = %v, %v", dead.Revoked, err)
	}
	alive, err := d.Entry(idPublic)
	if err != nil || alive.Revoked {
		t.Fatalf("public entry revoked = %v, %v", alive.Revoked, err)
	}

	if _, err := d.Add([]byte("TOKEN"), secretA); !errors.Is(err, ErrRevoked) {
		t.Fatalf("re-add revoked source: %v", err)
	}
}

func TestMultiLevelDerivationAccumulatesProvenance(t *testing.T) {
	d := New()
	originA := originOf("tenant-a", "input-a", false)
	originB := originOf("tenant-b", "input-b", false)
	originC := originOf("tenant-c", "input-c", true)

	a := mustAdd(t, d, "a", originA)
	b := mustAdd(t, d, "b", originB)

	level1 := mustDerive(t, d, "ab", a, b)
	e1, err := d.Entry(level1)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Depth != 1 {
		t.Fatalf("level1 depth = %d, want 1", e1.Depth)
	}
	if got := e1.SecretTenants(); !slices.Equal(got, []Tenant{"tenant-a", "tenant-b"}) {
		t.Fatalf("level1 secret tenants = %v", got)
	}

	c := mustAdd(t, d, "c", originC)
	level2 := mustDerive(t, d, "abc", level1, c)
	e2, err := d.Entry(level2)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Depth != 2 {
		t.Fatalf("level2 depth = %d, want 2", e2.Depth)
	}
	if got := e2.SecretTenants(); !slices.Equal(got, []Tenant{"tenant-a", "tenant-b"}) {
		t.Fatalf("level2 secret tenants = %v, want both secrets retained", got)
	}
	if !slices.ContainsFunc(e2.Origins, func(o Origin) bool { return o.Public }) {
		t.Fatal("public origin missing from provenance closure")
	}

	for _, auth := range [][]Tenant{
		{"tenant-a"},
		{"tenant-b"},
		{"tenant-a", "tenant-c"},
	} {
		snap, err := d.Snapshot(auth)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(snap.ByWord([]byte("abc")), level2) {
			t.Fatalf("derived secret leaked for authorization %v", auth)
		}
	}
	snap, err := d.Snapshot([]Tenant{"tenant-a", "tenant-b", "tenant-c"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snap.ByWord([]byte("abc")), level2) {
		t.Fatal("derived entry missing inside full authorization intersection")
	}

	if err := d.Revoke("tenant-a", originA.Input); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{a, level1, level2} {
		e, err := d.Entry(id)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Revoked {
			t.Fatalf("entry %d survived revocation of its transitive origin", id)
		}
	}

	if _, err := d.Derive([]byte("x"), level1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("derive from revoked parent: %v", err)
	}
	if _, err := d.Derive([]byte("x"), ID(9999)); !errors.Is(err, ErrUnknownEntry) {
		t.Fatalf("derive from unknown parent: %v", err)
	}
	if _, err := d.Derive([]byte("x")); !errors.Is(err, ErrNoParents) {
		t.Fatalf("derive without parents: %v", err)
	}
}

func TestPublicCannotDowngradeSecret(t *testing.T) {
	d := New()
	secret := originOf("tenant-a", "secret", false)
	public := originOf("tenant-b", "public", true)

	secretID := mustAdd(t, d, "X", secret)
	publicID := mustAdd(t, d, "X", public)
	derived := mustDerive(t, d, "X2", secretID, publicID)

	e, err := d.Entry(derived)
	if err != nil {
		t.Fatal(err)
	}
	if e.IsPublic() {
		t.Fatal("public origin downgraded a secret derivation")
	}

	if err := d.RevokeTenant("tenant-a"); err != nil {
		t.Fatal(err)
	}
	if e, _ := d.Entry(secretID); !e.Revoked {
		t.Fatal("RevokeTenant left secret entry live")
	}
	if e, _ := d.Entry(publicID); e.Revoked {
		t.Fatal("RevokeTenant revoked another tenant's public entry")
	}
	snapB, err := d.Snapshot([]Tenant{"tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	if snapB.Len() != 1 {
		t.Fatalf("tenant-b snapshot after tenant-a revocation = %d entries, want 1", snapB.Len())
	}

	if _, err := d.Derive([]byte("y"), publicID, secretID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("derivation mixing live and revoked: %v", err)
	}
}

func TestMergeStaysInsideAuthorization(t *testing.T) {
	src := New()
	originA := originOf("tenant-a", "input-a", false)
	originB := originOf("tenant-b", "input-b", false)

	a := mustAdd(t, src, "a", originA)
	b := mustAdd(t, src, "b", originB)
	derived := mustDerive(t, src, "ab", a, b)

	snapA, err := src.Snapshot([]Tenant{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Len() != 1 {
		t.Fatalf("scoped snapshot len = %d, want 1", snapA.Len())
	}

	dst := New()
	mapping, err := Merge(dst, snapA)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(mapping) != 1 {
		t.Fatalf("mapping size = %d, want 1", len(mapping))
	}

	snapDstB, err := dst.Snapshot([]Tenant{"tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	if snapDstB.Len() != 0 {
		t.Fatal("merge widened access beyond the snapshot intersection")
	}

	full, err := src.Snapshot([]Tenant{"tenant-a", "tenant-b"})
	if err != nil {
		t.Fatal(err)
	}
	dst2 := New()
	mapping2, err := Merge(dst2, full)
	if err != nil {
		t.Fatal(err)
	}
	mergedDerived, err := dst2.Entry(mapping2[derived])
	if err != nil {
		t.Fatal(err)
	}
	if mergedDerived.Depth != 1 || len(mergedDerived.Parents) != 2 {
		t.Fatalf("merged derivation lost shape: %+v", mergedDerived)
	}

	again, err := Merge(dst2, full)
	if err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(mapping2, again) {
		t.Fatalf("repeat merge changed ID mapping:\n%v\n%v", mapping2, again)
	}

	dst3 := New()
	if err := dst3.Revoke("tenant-a", originA.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dst3, full); !errors.Is(err, ErrRevoked) {
		t.Fatalf("merge past destination revocation: %v", err)
	}
	if _, err := Merge(dst3, nil); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("merge nil snapshot: %v", err)
	}
	if _, err := Merge(nil, full); !errors.Is(err, ErrNilDictionary) {
		t.Fatalf("merge nil dictionary: %v", err)
	}
}

func mapsEqual(a, b map[ID]ID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestSnapshotIsImmutableAndDefensive(t *testing.T) {
	d := New()
	originA := originOf("tenant-a", "input-a", false)
	mustAdd(t, d, "token", originA)

	snap, err := d.Snapshot([]Tenant{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}

	entries := snap.Entries()
	entries[0].Word[0] = 'Z'
	entries[0].Origins[0].Tenant = "attacker"
	again := snap.Entries()
	if string(again[0].Word) != "token" || again[0].Origins[0].Tenant != "tenant-a" {
		t.Fatal("snapshot mutated through returned entry copy")
	}

	words := snap.ByWord([]byte("token"))
	words[0] = ID(999)
	if got := snap.ByWord([]byte("token")); len(got) != 1 || got[0] == ID(999) {
		t.Fatal("snapshot index mutated through returned ID slice")
	}

	// Later dictionary mutations do not reach the captured snapshot.
	mustAdd(t, d, "token2", originOf("tenant-a", "input-a2", false))
	if err := d.Revoke("tenant-a", originA.Input); err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 1 {
		t.Fatalf("snapshot changed after revocation/add: len = %d", snap.Len())
	}
	if snap.Entries()[0].Revoked {
		t.Fatal("captured snapshot retroactively marked entry revoked")
	}

	// Dictionary entry copies are defensive too.
	view, err := d.Entry(1)
	if err != nil {
		t.Fatal(err)
	}
	view.Word[0] = 'Q'
	view.Origins[0].Tenant = "attacker"
	view2, _ := d.Entry(1)
	if string(view2.Word) != "token" || view2.Origins[0].Tenant != "tenant-a" {
		t.Fatal("Dictionary state mutated through Entry copy")
	}
}

func TestConcurrentSnapshotsAndMutations(t *testing.T) {
	d := New()
	tenants := []Tenant{"tenant-a", "tenant-b", "tenant-c"}

	// Seed one entry per tenant so snapshots always have work to do.
	for i, tenant := range tenants {
		mustAdd(t, d,
			fmtKey(i),
			originOf(string(tenant), fmt.Sprintf("seed-%d", i), false),
		)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for _, tenant := range tenants {
		tenant := tenant
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = d.Add(
					[]byte(fmt.Sprintf("%s-%d", tenant, n)),
					originOf(string(tenant), fmt.Sprintf("input-%s-%d", tenant, n), n%3 == 0),
				)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := d.Snapshot([]Tenant{tenant})
				if err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				entries := snap.Entries()
				for _, e := range entries {
					secrets := e.SecretTenants()
					if len(secrets) == 1 && secrets[0] != tenant {
						t.Errorf("snapshot for %s exposed secret of %s", tenant, secrets[0])
						return
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 50; n++ {
			_ = d.Revoke("tenant-a", SumDigest([]byte(fmt.Sprintf("input-tenant-a-%d", n*7))))
		}
	}()

	// Run the workload briefly, then stop and wait. The race detector is the
	// main assertion here.
	for i := 0; i < 200; i++ {
		snap, err := d.Snapshot(tenants)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Len() < 0 {
			t.Fatal("negative snapshot length")
		}
	}
	close(stop)
	wg.Wait()

	stats := d.Stats()
	if stats.Total != stats.Live+stats.Revoked {
		t.Fatalf("inconsistent stats: %+v", stats)
	}
}

func fmtKey(i int) string {
	return "seed-" + string(rune('a'+i))
}

func TestDigestAndOriginsDeterministic(t *testing.T) {
	d1 := SumDigest([]byte("same-input"))
	d2 := SumDigest([]byte("same-input"))
	d3 := SumDigest([]byte("other"))
	if d1 != d2 || d1 == d3 {
		t.Fatal("digest not content-addressed")
	}
	if d1.IsZero() || (Digest{}).IsZero() == false {
		t.Fatal("zero digest handling wrong")
	}
	if len(d1.Hex()) != DigestSize*2 {
		t.Fatal("unexpected digest encoding length")
	}

	d := New()
	unsorted := []Origin{
		originOf("tenant-b", "in-b", false),
		originOf("tenant-a", "in-a", true),
		originOf("tenant-a", "in-a", true),
		originOf("tenant-a", "in-a", false),
	}
	id, err := d.importEntry([]byte("w"), unsorted, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := d.Entry(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Origins) != 3 {
		t.Fatalf("origin dedup = %d entries, want 3 (public/secret kept distinct)", len(e.Origins))
	}
	want := []Origin{
		originOf("tenant-a", "in-a", false),
		originOf("tenant-a", "in-a", true),
		originOf("tenant-b", "in-b", false),
	}
	if !slices.Equal(e.Origins, want) {
		t.Fatalf("origin order = %+v, want %+v", e.Origins, want)
	}
}
