package verifysched

import (
	"reflect"
	"testing"
)

var threeChecks = []CheckSpec{
	{Name: "lint", Validator: "golint", Version: "1.0"},
	{Name: "unit", Validator: "gotest", Version: "2.1"},
	{Name: "e2e", Validator: "runner", Version: "0.9"},
}

func mustFreeze(t *testing.T, s *Scheduler, digest string, checks []CheckSpec) Bundle {
	t.Helper()
	b, err := s.Freeze(digest, checks)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return b
}

func mustDispatch(t *testing.T, s *Scheduler) map[string]Dispatch {
	t.Helper()
	ds, err := s.Dispatch()
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	out := make(map[string]Dispatch, len(ds))
	for _, d := range ds {
		out[d.CheckName] = d
	}
	return out
}

func passReceipt(b Bundle, d Dispatch, version string) Receipt {
	return Receipt{
		BundleID:         b.ID,
		CheckName:        d.CheckName,
		RunID:            d.RunID,
		InputDigest:      b.InputDigest,
		ValidatorVersion: version,
		Status:           Passed,
	}
}

func versions(checks []CheckSpec) map[string]string {
	m := make(map[string]string, len(checks))
	for _, c := range checks {
		m[c.Name] = c.Version
	}
	return m
}

func TestOutOfOrderReceipts(t *testing.T) {
	s := New()
	b := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)
	disp := mustDispatch(t, s)
	if len(disp) != 3 {
		t.Fatalf("expected 3 dispatches, got %d", len(disp))
	}

	// Receipts arrive in reverse completion order.
	order := []string{"e2e", "unit", "lint"}
	for i, name := range order {
		if err := s.Submit(passReceipt(b, disp[name], ver[name])); err != nil {
			t.Fatalf("Submit(%s): %v", name, err)
		}
		rep, err := s.Gate()
		if err != nil {
			t.Fatalf("Gate: %v", err)
		}
		if i < len(order)-1 {
			if rep.Passed {
				t.Fatalf("gate passed after %d/%d receipts", i+1, len(order))
			}
			wantPending := len(order) - i - 1
			if len(rep.Pending) != wantPending {
				t.Fatalf("expected %d pending checks, got %v", wantPending, rep.Pending)
			}
		}
	}
	rep, err := s.Gate()
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !rep.Passed {
		t.Fatalf("gate should pass once all receipts arrived, got %+v", rep)
	}
}

func TestPartialPass(t *testing.T) {
	s := New()
	b := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)
	disp := mustDispatch(t, s)

	for _, name := range []string{"lint", "e2e"} {
		if err := s.Submit(passReceipt(b, disp[name], ver[name])); err != nil {
			t.Fatalf("Submit(%s): %v", name, err)
		}
	}
	fail := passReceipt(b, disp["unit"], ver["unit"])
	fail.Status = Failed
	fail.Message = "1 test failed"
	if err := s.Submit(fail); err != nil {
		t.Fatalf("Submit(unit): %v", err)
	}

	rep, err := s.Gate()
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if rep.Passed {
		t.Fatal("gate must not pass with a failed check")
	}
	if !reflect.DeepEqual(rep.Failed, []string{"unit"}) {
		t.Fatalf("expected Failed=[unit], got %v", rep.Failed)
	}
	if len(rep.Pending) != 0 {
		t.Fatalf("expected no pending checks, got %v", rep.Pending)
	}
}

func TestRevokedCheck(t *testing.T) {
	s := New()
	b := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)
	disp := mustDispatch(t, s)

	for _, name := range []string{"lint", "unit"} {
		if err := s.Submit(passReceipt(b, disp[name], ver[name])); err != nil {
			t.Fatalf("Submit(%s): %v", name, err)
		}
	}
	rep, _ := s.Gate()
	if rep.Passed || !reflect.DeepEqual(rep.Pending, []string{"e2e"}) {
		t.Fatalf("gate should be blocked on e2e, got %+v", rep)
	}

	if err := s.Revoke("e2e"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	rep, _ = s.Gate()
	if !rep.Passed {
		t.Fatalf("gate should pass once the blocking check is revoked, got %+v", rep)
	}
	if !reflect.DeepEqual(rep.Revoked, []string{"e2e"}) {
		t.Fatalf("expected Revoked=[e2e], got %v", rep.Revoked)
	}

	// A revoked check is no longer dispatched and its receipts are rejected.
	if ds, _ := s.Dispatch(); len(ds) != 0 {
		t.Fatalf("revoked check must not be dispatched, got %v", ds)
	}
	if err := s.Submit(passReceipt(b, disp["e2e"], ver["e2e"])); err != ErrRevokedCheck {
		t.Fatalf("expected ErrRevokedCheck, got %v", err)
	}
	if err := s.Revoke("nope"); err != ErrUnknownCheck {
		t.Fatalf("expected ErrUnknownCheck, got %v", err)
	}
}

func TestStaleResultsArchivedOnInputOrRuleChange(t *testing.T) {
	s := New()
	b1 := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)
	disp1 := mustDispatch(t, s)
	for _, name := range []string{"lint", "unit", "e2e"} {
		if err := s.Submit(passReceipt(b1, disp1[name], ver[name])); err != nil {
			t.Fatalf("Submit(%s): %v", name, err)
		}
	}
	if rep, _ := s.Gate(); !rep.Passed {
		t.Fatal("bundle-1 gate should pass")
	}

	// Input change: a new bundle is frozen, the old one is archived with
	// its results, and nothing carries over to the new gate.
	b2 := mustFreeze(t, s, "sha256:input-v2", threeChecks)
	arch := s.Archived()
	if len(arch) != 1 || arch[0].Bundle.ID != b1.ID {
		t.Fatalf("expected bundle-1 archived, got %+v", arch)
	}
	if len(arch[0].Receipts) != 3 {
		t.Fatalf("expected 3 archived receipts, got %d", len(arch[0].Receipts))
	}
	rep, _ := s.Gate()
	if rep.Passed || len(rep.Pending) != 3 {
		t.Fatalf("old results must not satisfy the new gate, got %+v", rep)
	}

	// A receipt bound to the archived bundle is archived, never counted.
	late := passReceipt(b1, disp1["lint"], ver["lint"])
	late.RunID = disp1["lint"].RunID
	if err := s.Submit(late); err != ErrStaleBundle {
		t.Fatalf("expected ErrStaleBundle, got %v", err)
	}
	if got := s.Archived()[0].Receipts; len(got) != 4 {
		t.Fatalf("late receipt should be archived, got %d receipts", len(got))
	}

	// Receipts bound to stale inputs or validator versions are rejected.
	disp2 := mustDispatch(t, s)
	staleInput := passReceipt(b2, disp2["lint"], ver["lint"])
	staleInput.InputDigest = "sha256:input-v1"
	if err := s.Submit(staleInput); err != ErrStaleInput {
		t.Fatalf("expected ErrStaleInput, got %v", err)
	}
	staleVer := passReceipt(b2, disp2["lint"], "0.1")
	if err := s.Submit(staleVer); err != ErrStaleValidator {
		t.Fatalf("expected ErrStaleValidator, got %v", err)
	}

	// Rule change (validator version bump) freezes yet another bundle.
	changed := []CheckSpec{
		{Name: "lint", Validator: "golint", Version: "1.1"},
		{Name: "unit", Validator: "gotest", Version: "2.1"},
		{Name: "e2e", Validator: "runner", Version: "0.9"},
	}
	b3 := mustFreeze(t, s, "sha256:input-v2", changed)
	if b3.RuleDigest == b2.RuleDigest {
		t.Fatal("rule digest must change when validator versions change")
	}
	if len(s.Archived()) != 2 {
		t.Fatalf("expected 2 archived bundles, got %d", len(s.Archived()))
	}
	if rep, _ := s.Gate(); rep.Passed || len(rep.Pending) != 3 {
		t.Fatalf("bundle-2 results must not satisfy bundle-3, got %+v", rep)
	}
}

func TestErrorReuse(t *testing.T) {
	s := New()
	b := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)
	disp := mustDispatch(t, s)

	// An errored run does not satisfy the gate.
	bad := passReceipt(b, disp["lint"], ver["lint"])
	bad.Status = Errored
	bad.Message = "validator crashed"
	if err := s.Submit(bad); err != nil {
		t.Fatalf("Submit(errored): %v", err)
	}
	rep, _ := s.Gate()
	if rep.Passed || !reflect.DeepEqual(rep.Errored, []string{"lint"}) {
		t.Fatalf("errored check must block the gate, got %+v", rep)
	}

	// Re-dispatch issues a fresh run id; the old one cannot be replayed.
	disp2 := mustDispatch(t, s)
	if len(disp2) != 3 { // lint re-issued, unit+e2e still pending
		t.Fatalf("expected 3 dispatches, got %d", len(disp2))
	}
	if disp2["lint"].RunID == disp["lint"].RunID {
		t.Fatal("errored check must be re-issued with a new run id")
	}
	replay := passReceipt(b, disp["lint"], ver["lint"])
	if err := s.Submit(replay); err != ErrRunMismatch {
		t.Fatalf("expected ErrRunMismatch for replayed run id, got %v", err)
	}

	// The fresh run completes; a duplicate result is rejected.
	if err := s.Submit(passReceipt(b, disp2["lint"], ver["lint"])); err != nil {
		t.Fatalf("Submit(retry): %v", err)
	}
	if err := s.Submit(passReceipt(b, disp2["lint"], ver["lint"])); err != ErrDuplicateResult {
		t.Fatalf("expected ErrDuplicateResult, got %v", err)
	}
	for _, name := range []string{"unit", "e2e"} {
		if err := s.Submit(passReceipt(b, disp2[name], ver[name])); err != nil {
			t.Fatalf("Submit(%s): %v", name, err)
		}
	}
	if rep, _ := s.Gate(); !rep.Passed {
		t.Fatalf("gate should pass after retry, got %+v", rep)
	}
}

func TestChecksMustShareOneFrozenBundle(t *testing.T) {
	s := New()
	b := mustFreeze(t, s, "sha256:input-v1", threeChecks)
	ver := versions(threeChecks)

	// A check that is not part of the frozen bundle is rejected.
	r := Receipt{
		BundleID:         b.ID,
		CheckName:        "coverage",
		RunID:            "bundle-1/coverage#1",
		InputDigest:      b.InputDigest,
		ValidatorVersion: "1.0",
		Status:           Passed,
	}
	if err := s.Submit(r); err != ErrUnknownCheck {
		t.Fatalf("expected ErrUnknownCheck, got %v", err)
	}
	// A receipt for a bundle that was never frozen is rejected.
	r.CheckName = "lint"
	r.BundleID = "bundle-99"
	if err := s.Submit(r); err != ErrUnknownBundle {
		t.Fatalf("expected ErrUnknownBundle, got %v", err)
	}
	// The gate only ever evaluates the current frozen bundle.
	rep, _ := s.Gate()
	if rep.BundleID != b.ID || len(rep.Pending) != 3 {
		t.Fatalf("unexpected gate report %+v", rep)
	}
	_ = ver
}
