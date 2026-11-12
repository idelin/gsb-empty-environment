package netsec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const snapshotGoldenFile = "testdata/snapshot_golden.json"

type goldenSnapshots struct {
	SchemaVersion int        `json:"schema_version"`
	Cases         []Snapshot `json:"cases"`
}

// migrationFixture builds the pre-migration query: the account sits under ou-a
// with ordinary allow boundaries.
func migrationFixture() EvalInput {
	identity := Policy{
		Name: "identity",
		Statements: []Statement{
			{
				Name:      "read-allow",
				Effect:    EffectAllow,
				Actions:   []Action{actRead},
				Resources: NewIntervalSet(iv(1, 100)),
			},
		},
	}
	return EvalInput{
		Action:         actRead,
		Resource:       40,
		IdentityPolicy: identity,
		ResourcePolicy: Policy{Name: "resource"},
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
			{NodeID: "ou-a", Effect: EffectAllow, Resources: NewIntervalSet(iv(10, 80))},
			{NodeID: "acct-1", Effect: EffectAllow, Resources: NewIntervalSet(iv(20, 60))},
		},
	}
}

// emptyAllowFixture exercises a layer whose allow boundary covers nothing.
func emptyAllowFixture() EvalInput {
	in := migrationFixture()
	in.Resource = 5
	in.Layers = []Layer{
		{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		{NodeID: "ou-empty", Effect: EffectAllow, Resources: IntervalSet{}},
		{NodeID: "acct-1", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
	}
	return in
}

// TestSnapshotGoldenFile locks the serialized shape and the historical
// verdicts. Regenerate intentionally with GSB_REGEN_GOLDEN=1 after an
// acknowledged versioned schema change.
func TestSnapshotGoldenFile(t *testing.T) {
	engine := testEngine(t)
	fixtures := []EvalInput{migrationFixture(), emptyAllowFixture()}

	generated := goldenSnapshots{SchemaVersion: SnapshotVersion}
	for _, in := range fixtures {
		decision, err := engine.Evaluate(in)
		if err != nil {
			t.Fatal(err)
		}
		generated.Cases = append(generated.Cases, NewSnapshot(in, decision))
	}
	generatedJSON, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if os.Getenv("GSB_REGEN_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(snapshotGoldenFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotGoldenFile, append(generatedJSON, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("golden snapshot regenerated")
	}

	storedJSON, err := os.ReadFile(snapshotGoldenFile)
	if err != nil {
		t.Fatalf("golden snapshot missing; run GSB_REGEN_GOLDEN=1 go test: %v", err)
	}
	if string(storedJSON) != string(generatedJSON)+"\n" {
		t.Fatalf("snapshot drift detected\n--- stored ---\n%s\n--- current ---\n%s",
			storedJSON, generatedJSON)
	}
}

// TestOldQuerySnapshotUnaffectedByMigration replays stored snapshots after the
// account moves to a different organization branch. The historical query and
// its verdict must reproduce exactly; only fresh queries see the new tree.
func TestOldQuerySnapshotUnaffectedByMigration(t *testing.T) {
	engine := testEngine(t)

	storedJSON, err := os.ReadFile(snapshotGoldenFile)
	if err != nil {
		t.Fatalf("golden snapshot missing; run GSB_REGEN_GOLDEN=1 go test: %v", err)
	}
	var stored goldenSnapshots
	if err := json.Unmarshal(storedJSON, &stored); err != nil {
		t.Fatal(err)
	}

	migratedLayers := []Layer{
		{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		{NodeID: "ou-b", Effect: EffectDeny, Resources: NewIntervalSet(iv(30, 50))},
		{NodeID: "acct-9", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
	}

	// Resource 40 lands inside the migrated ou-b refusal; resource 5 does not,
	// so the two cases pin both sides of a fresh post-migration decision.
	migrationBlocked := map[int64]bool{40: true, 5: false}

	for idx, snap := range stored.Cases {
		// Re-evaluate the exact input captured in the old snapshot.
		replayed, err := engine.Evaluate(snap.Input)
		if err != nil {
			t.Fatalf("case %d: replay failed: %v", idx, err)
		}
		if replayed.Allowed != snap.Decision.Allowed {
			t.Fatalf("case %d: verdict changed from %v to %v after migration",
				idx, snap.Decision.Allowed, replayed.Allowed)
		}
		if !replayed.EffectiveAllow.Equal(snap.Decision.EffectiveAllow) {
			t.Fatalf("case %d: effective allow drifted", idx)
		}
		if len(replayed.Layers) != len(snap.Decision.Layers) {
			t.Fatalf("case %d: layer contribution count drifted", idx)
		}

		// The same query issued against the migrated tree is a new decision and
		// must observe the new boundaries.
		currentInput := snap.Input
		currentInput.Layers = migratedLayers
		current, err := engine.Evaluate(currentInput)
		if err != nil {
			t.Fatalf("case %d: current evaluation failed: %v", idx, err)
		}
		if wantBlocked := migrationBlocked[snap.Input.Resource]; wantBlocked != !current.Allowed {
			t.Fatalf("case %d (resource %d): fresh verdict = allowed:%v, want blocked:%v",
				idx, snap.Input.Resource, current.Allowed, wantBlocked)
		}
		if migrationBlocked[snap.Input.Resource] &&
			(current.EarliestDeny == nil || current.EarliestDeny.Source != "ou-b") {
			t.Fatalf("case %d: expected ou-b deny evidence, got %+v",
				idx, current.EarliestDeny)
		}

		// Round-trip the old snapshot once more to prove persistence stability.
		data, err := MarshalSnapshot(snap)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalSnapshot(data)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Decision.Allowed != snap.Decision.Allowed {
			t.Fatalf("case %d: snapshot round-trip changed the verdict", idx)
		}
	}
}

// TestEmptyAllowLayerSnapshotStaysDenied specifically pins the empty allow
// layer case so a later engine change cannot silently turn it into an allow.
func TestEmptyAllowLayerSnapshotStaysDenied(t *testing.T) {
	engine := testEngine(t)
	decision, err := engine.Evaluate(emptyAllowFixture())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("empty allow layer snapshot must remain denied")
	}
	if decision.EarliestDeny == nil ||
		decision.EarliestDeny.Reason != ReasonEmptyAllow ||
		decision.EarliestDeny.Source != "ou-empty" {
		t.Fatalf("unexpected evidence %+v", decision.EarliestDeny)
	}

	data, err := MarshalSnapshot(NewSnapshot(emptyAllowFixture(), decision))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Decision.Allowed || decoded.Decision.EarliestDeny == nil {
		t.Fatal("decoded snapshot lost the empty-allow denial")
	}
}
