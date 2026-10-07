package csvq

import "testing"

// importTwoBatches imports a source whose second batch widens the code
// column from int to text, producing a two-version schema history, and
// which contains one quarantined row.
func importTwoBatches(t *testing.T) ([]byte, *Store) {
	t.Helper()
	src1 := "id,code\n1,007\n2,042,extra\n3,010\n" // seq 1 quarantined
	src2 := "id,code\n4,abc\n"
	full := []byte(src1 + "4,abc\n")

	tab1 := mustParse(t, src1)
	tab2 := mustParse(t, src2)

	s := NewStore()
	if _, err := s.Import(tab1); err != nil {
		t.Fatal(err)
	}
	// Rebase the second batch's sequences so both batches cover the
	// concatenated source, as a real incremental importer would.
	recs := make([]Record, len(tab2.Records))
	for i, r := range tab2.Records {
		r.Seq += 3
		recs[i] = r
	}
	if _, err := s.ImportBatch(tab2.Header, recs, nil); err != nil {
		t.Fatal(err)
	}
	return full, s
}

func TestRecoveryManifestRoundTrip(t *testing.T) {
	src, s := importTwoBatches(t)
	m := BuildManifest(src, s)

	if m.SchemaHistory[len(m.SchemaHistory)-1].Version != 2 {
		t.Fatalf("manifest schema history = %+v", m.SchemaHistory)
	}
	if len(m.Batches) != 2 {
		t.Fatalf("manifest batches = %d, want 2", len(m.Batches))
	}
	if len(m.Quarantine) != 1 || m.Quarantine[0].Seq != 1 {
		t.Fatalf("manifest quarantine = %+v", m.Quarantine)
	}
	if m.Records != 3 {
		t.Fatalf("manifest records = %d, want 3", m.Records)
	}

	// Full re-import of the same source must reproduce the manifest.
	if err := VerifyRecovery(src, m); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryDetectsSourceTamper(t *testing.T) {
	src, s := importTwoBatches(t)
	m := BuildManifest(src, s)

	bad := append([]byte(nil), src...)
	bad[0] = 'X'
	if err := VerifyRecovery(bad, m); err == nil {
		t.Fatal("tampered source not detected")
	}
}

func TestRecoveryDetectsManifestTamper(t *testing.T) {
	src, s := importTwoBatches(t)
	m := BuildManifest(src, s)

	m.SchemaHistory = append(m.SchemaHistory, Schema{Version: 3})
	if err := VerifyRecovery(src, m); err == nil {
		t.Fatal("tampered schema history not detected")
	}
}

func TestRecoveryRejectsForeignSource(t *testing.T) {
	other := mustParse(t, "id,code\n9,9\n")
	s2 := NewStore()
	if _, err := s2.Import(other); err != nil {
		t.Fatal(err)
	}
	m := BuildManifest([]byte("id,code\n9,9\n"), s2)
	// Manifest bound to another source must not verify against ours.
	src, _ := importTwoBatches(t)
	if err := VerifyRecovery(src, m); err == nil {
		t.Fatal("foreign manifest verified against wrong source")
	}
}
