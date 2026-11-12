package netsec

import (
	"errors"
	"testing"
)

// sealWindow fills every partition of manifest through barrier and returns
// the finalized attestation.
func sealWindow(t *testing.T, manifest Manifest, signer Signer) (*Barrier, *Attestation) {
	t.Helper()
	barrier, err := NewBarrier(manifest)
	if err != nil {
		t.Fatalf("NewBarrier: %v", err)
	}
	for _, spec := range manifest.Partitions {
		seals := chain(manifest.Window, spec.Partition, spec.UpperBound, spec.Anchor)
		if err := barrier.SubmitPrefix(PrefixProof{Partition: spec.Partition, Seals: seals}); err != nil {
			t.Fatalf("SubmitPrefix %q: %v", spec.Partition, err)
		}
	}
	att, err := barrier.Finalize(signer)
	if err != nil {
		t.Fatalf("Finalize window %d: %v", manifest.Window, err)
	}
	return barrier, att
}

func TestCoordinatorWindowChainingAndReplayRejection(t *testing.T) {
	manifest := genesisManifest()
	coordinator, err := NewCoordinator(manifest)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := GenerateEd25519Key("root-key")

	barrier := coordinator.Barrier()
	alpha := chain(1, "alpha", 3, Digest{})
	beta := chain(1, "beta", 2, Digest{})
	for _, seal := range alpha {
		if _, err := barrier.Submit(seal); err != nil {
			t.Fatal(err)
		}
	}
	for _, seal := range beta {
		if _, err := barrier.Submit(seal); err != nil {
			t.Fatal(err)
		}
	}
	first, err := barrier.Finalize(key)
	if err != nil {
		t.Fatal(err)
	}

	// Advance without committing the attestation must fail.
	if _, err := coordinator.Advance(2, map[PartitionID]Seq{"alpha": 1, "beta": 1}, first); !errors.Is(err, ErrUnknownWindow) {
		t.Fatalf("Advance before Commit: want ErrUnknownWindow, got %v", err)
	}
	if err := coordinator.Commit(first); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Window ids must move forward.
	if _, err := coordinator.Advance(1, map[PartitionID]Seq{"alpha": 1, "beta": 1}, first); !errors.Is(err, ErrUnknownWindow) {
		t.Fatalf("Advance to same window: want ErrUnknownWindow, got %v", err)
	}

	next, err := coordinator.Advance(2, map[PartitionID]Seq{"alpha": 2, "beta": 1}, first)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}

	// The new manifest chains onto the signed previous root.
	current := coordinator.CurrentManifest()
	if current.Window != 2 || current.PrevRoot != first.Root {
		t.Fatalf("new manifest not chained: %+v", current)
	}

	// Continuing partitions anchor on their previous terminal root.
	var alphaSpec, betaSpec PartitionSpec
	for _, spec := range current.Partitions {
		switch spec.Partition {
		case "alpha":
			alphaSpec = spec
		case "beta":
			betaSpec = spec
		}
	}
	if alphaSpec.Anchor != alpha[2].Digest || betaSpec.Anchor != beta[1].Digest {
		t.Fatal("continuing partition anchors do not match prior terminal roots")
	}

	// A window-1 seal must be rejected by the window-2 barrier: no mixed
	// generations.
	oldSeal := NewSeal(1, "alpha", 1, Digest{})
	if _, err := next.Submit(oldSeal); !errors.Is(err, ErrForeignWindow) {
		t.Fatalf("replay old seal: want ErrForeignWindow, got %v", err)
	}

	// Window-2 seals chain from the window-1 terminal roots.
	windowTwoAlpha := chain(2, "alpha", 2, alphaSpec.Anchor)
	windowTwoBeta := chain(2, "beta", 1, betaSpec.Anchor)
	if err := next.SubmitPrefix(PrefixProof{Partition: "alpha", Seals: windowTwoAlpha}); err != nil {
		t.Fatalf("window 2 alpha: %v", err)
	}
	if err := next.SubmitPrefix(PrefixProof{Partition: "beta", Seals: windowTwoBeta}); err != nil {
		t.Fatalf("window 2 beta: %v", err)
	}
	second, err := next.Finalize(key)
	if err != nil {
		t.Fatalf("Finalize window 2: %v", err)
	}
	if second.PrevRoot != first.Root {
		t.Fatal("second window root does not chain onto the first")
	}
	verifier, _ := NewEd25519Verifier(key.KeyID(), key.PublicKey())
	if err := second.Verify(&current, verifier); err != nil {
		t.Fatalf("verify window 2: %v", err)
	}
}

func TestCoordinatorMigrationsApplyAtNextWindowOnly(t *testing.T) {
	coordinator, err := NewCoordinator(genesisManifest())
	if err != nil {
		t.Fatal(err)
	}

	migrationAnchor := Digest{0x0C}
	migrations := []Migration{
		{Kind: MigrationAdd, Partition: "gamma", Anchor: Digest{0x07}},
		{Kind: MigrationMigrate, Partition: "beta", Anchor: migrationAnchor},
		{Kind: MigrationRemove, Partition: "alpha"},
	}
	if err := coordinator.SetMigration(migrations...); err != nil {
		t.Fatalf("SetMigration: %v", err)
	}

	// The active window must remain untouched while the migration is pending.
	active := coordinator.CurrentManifest()
	if len(active.Partitions) != 2 {
		t.Fatalf("active window changed before Advance: %+v", active)
	}
	if got := coordinator.PendingMigrations(); len(got) != 3 {
		t.Fatalf("pending migrations = %d", len(got))
	}

	_, first := sealWindow(t, active, nil)
	if err := coordinator.Commit(first); err != nil {
		t.Fatal(err)
	}
	bounds := map[PartitionID]Seq{
		"beta":  1,
		"gamma": 2,
	}
	barrier, err := coordinator.Advance(2, bounds, first)
	if err != nil {
		t.Fatalf("Advance with migrations: %v", err)
	}

	current := coordinator.CurrentManifest()
	specs := map[PartitionID]PartitionSpec{}
	for _, spec := range current.Partitions {
		specs[spec.Partition] = spec
	}
	if _, ok := specs["alpha"]; ok {
		t.Fatal("removed partition alpha still present")
	}
	if specs["beta"].Anchor != migrationAnchor {
		t.Fatalf("migrated beta anchor = %v, want %v", specs["beta"].Anchor, migrationAnchor)
	}
	if specs["gamma"].Anchor != (Digest{0x07}) || specs["gamma"].UpperBound != 2 {
		t.Fatalf("added gamma spec wrong: %+v", specs["gamma"])
	}
	if len(coordinator.PendingMigrations()) != 0 {
		t.Fatal("pending migrations were not consumed by Advance")
	}

	// New-window seals must use the post-migration anchors.
	if err := barrier.SubmitPrefix(PrefixProof{
		Partition: "beta",
		Seals:     chain(2, "beta", 1, migrationAnchor),
	}); err != nil {
		t.Fatalf("beta after migration: %v", err)
	}
	if err := barrier.SubmitPrefix(PrefixProof{
		Partition: "gamma",
		Seals:     chain(2, "gamma", 2, Digest{0x07}),
	}); err != nil {
		t.Fatalf("gamma new partition: %v", err)
	}
	att, err := barrier.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize migrated window: %v", err)
	}
	if _, ok := att.RootFor("alpha"); ok {
		t.Fatal("removed partition appears in migrated window root")
	}
}

func TestCoordinatorRejectsInvalidMigrations(t *testing.T) {
	coordinator, _ := NewCoordinator(genesisManifest())

	cases := [][]Migration{
		{{Kind: MigrationAdd, Partition: "alpha"}},
		{{Kind: MigrationMigrate, Partition: "ghost"}},
		{{Kind: MigrationRemove, Partition: "ghost"}},
		{{Kind: MigrationKind(99), Partition: "alpha"}},
		{{Kind: MigrationAdd, Partition: ""}},
		{
			{Kind: MigrationAdd, Partition: "gamma"},
			{Kind: MigrationAdd, Partition: "gamma"},
		},
	}
	for idx, migrations := range cases {
		if err := coordinator.SetMigration(migrations...); !errors.Is(err, ErrInvalidMigration) {
			t.Fatalf("case %d: want ErrInvalidMigration, got %v", idx, err)
		}
	}

	// Advance must reject incomplete upper-bound coverage.
	if err := coordinator.SetMigration(Migration{Kind: MigrationAdd, Partition: "gamma"}); err != nil {
		t.Fatal(err)
	}
	_, first := sealWindow(t, genesisManifest(), nil)
	if err := coordinator.Commit(first); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Advance(2, map[PartitionID]Seq{"alpha": 1}, first); !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("Advance missing bounds: want ErrInvalidMigration, got %v", err)
	}
	coordinator.ClearMigrations()
}

func TestCoordinatorRejectsForeignAttestationCommit(t *testing.T) {
	coordinator, _ := NewCoordinator(genesisManifest())
	foreign := &Attestation{Window: 42, Root: Digest{0x01}}
	if err := coordinator.Commit(foreign); !errors.Is(err, ErrUnknownWindow) {
		t.Fatalf("Commit foreign attestation: want ErrUnknownWindow, got %v", err)
	}
	if err := coordinator.Commit(nil); !errors.Is(err, ErrUnknownWindow) {
		t.Fatalf("Commit nil: want ErrUnknownWindow, got %v", err)
	}
}

func TestCoordinatorGenesisRequiresZeroPrevRoot(t *testing.T) {
	manifest := genesisManifest()
	manifest.PrevRoot = Digest{0x09}
	if _, err := NewCoordinator(manifest); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("genesis with prev root: want ErrInvalidManifest, got %v", err)
	}
}
