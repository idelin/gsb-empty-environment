package verisched

import "testing"

func specs() []CheckSpec {
	return []CheckSpec{
		{ID: "lint", Validator: "lintval", Version: "1.0.0"},
		{ID: "unit", Validator: "testval", Version: "2.1.0"},
		{ID: "secscan", Validator: "secval", Version: "0.9.3"},
	}
}

func passReceipt(pkg Package, run Run, checkID string) Receipt {
	spec, _ := pkg.Spec(checkID)
	return Receipt{
		PackageID:        pkg.ID,
		CheckID:          checkID,
		RunID:            run.ID,
		InputDigest:      pkg.InputDigest,
		ValidatorVersion: spec.Version,
		Status:           StatusPassed,
	}
}

func mustFreeze(t *testing.T, s *Scheduler, digest string, checks []CheckSpec) Package {
	pkg, err := s.FreezePackage(digest, checks)
	if err != nil {
		t.Fatalf("FreezePackage: %v", err)
	}
	return pkg
}

func mustRun(t *testing.T, s *Scheduler) Run {
	run, err := s.StartRun()
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return run
}

// Receipts arriving out of order must still satisfy the gate once every
// check of the frozen package has a passing, correctly bound receipt.
func TestOutOfOrderReceipts(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run := mustRun(t, s)

	for _, id := range []string{"secscan", "lint", "unit"} {
		if err := s.Submit(passReceipt(pkg, run, id)); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
		rep, err := s.Gate(run.ID)
		if err != nil {
			t.Fatalf("Gate: %v", err)
		}
		if id != "unit" && rep.Passed {
			t.Fatalf("gate passed before all receipts arrived (after %s)", id)
		}
	}
	rep, err := s.Gate(run.ID)
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !rep.Passed {
		t.Fatalf("gate should pass after all receipts, states: %v", rep.Checks)
	}
	for _, id := range []string{"lint", "unit", "secscan"} {
		if rep.Checks[id] != CheckPassed {
			t.Fatalf("check %s state = %v, want passed", id, rep.Checks[id])
		}
	}
}

// A gate with one failed and one missing check must stay closed and
// report each check's state precisely.
func TestPartialPass(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run := mustRun(t, s)

	if err := s.Submit(passReceipt(pkg, run, "lint")); err != nil {
		t.Fatalf("Submit lint: %v", err)
	}
	failed := passReceipt(pkg, run, "unit")
	failed.Status = StatusFailed
	if err := s.Submit(failed); err != nil {
		t.Fatalf("Submit unit: %v", err)
	}

	rep, err := s.Gate(run.ID)
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if rep.Passed {
		t.Fatal("gate must not pass on partial results")
	}
	if rep.Checks["lint"] != CheckPassed {
		t.Fatalf("lint state = %v, want passed", rep.Checks["lint"])
	}
	if rep.Checks["unit"] != CheckFailed {
		t.Fatalf("unit state = %v, want failed", rep.Checks["unit"])
	}
	if rep.Checks["secscan"] != CheckPending {
		t.Fatalf("secscan state = %v, want pending", rep.Checks["secscan"])
	}
}

// Revoking a check invalidates its receipts and closes the gate; new
// receipts for the revoked check are rejected.
func TestRevokedCheck(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run := mustRun(t, s)
	for _, id := range []string{"lint", "unit", "secscan"} {
		if err := s.Submit(passReceipt(pkg, run, id)); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
	}
	rep, _ := s.Gate(run.ID)
	if !rep.Passed {
		t.Fatal("gate should pass before revocation")
	}

	if err := s.RevokeCheck("unit"); err != nil {
		t.Fatalf("RevokeCheck: %v", err)
	}
	rep, _ = s.Gate(run.ID)
	if rep.Passed {
		t.Fatal("gate must close after a check is revoked")
	}
	if rep.Checks["unit"] != CheckRevoked {
		t.Fatalf("unit state = %v, want revoked", rep.Checks["unit"])
	}
	if err := s.Submit(passReceipt(pkg, run, "unit")); err != ErrCheckRevoked {
		t.Fatalf("resubmit revoked check: err = %v, want ErrCheckRevoked", err)
	}
	if err := s.RevokeCheck("nope"); err != ErrUnknownCheck {
		t.Fatalf("revoke unknown check: err = %v, want ErrUnknownCheck", err)
	}
}

// After the input changes, the old package is archived: its receipts are
// kept for audit but cannot satisfy the new gate, and replaying them
// against the new package is a binding mismatch.
func TestErroneousReuseAfterInputChange(t *testing.T) {
	s := NewScheduler()
	oldPkg := mustFreeze(t, s, "sha256:input-v1", specs())
	oldRun := mustRun(t, s)
	for _, id := range []string{"lint", "unit", "secscan"} {
		if err := s.Submit(passReceipt(oldPkg, oldRun, id)); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
	}
	rep, _ := s.Gate(oldRun.ID)
	if !rep.Passed {
		t.Fatal("old gate should pass before the input change")
	}

	newPkg := mustFreeze(t, s, "sha256:input-v2", specs())
	if newPkg.ID == oldPkg.ID {
		t.Fatal("input change must produce a new frozen package")
	}
	newRun := mustRun(t, s)

	// Replaying an old receipt against the old (now archived) package is
	// recorded for audit only.
	stale := passReceipt(oldPkg, oldRun, "lint")
	stale.RunID = "run-audit"
	if err := s.Submit(stale); err != ErrPackageArchived {
		t.Fatalf("submit to archived package: err = %v, want ErrPackageArchived", err)
	}
	if _, ok := s.ArchivedReceipt(oldPkg.ID, "run-audit", "lint"); !ok {
		t.Fatal("archived receipt should be retained for audit")
	}

	// Forging the new package id onto an old receipt must not bind:
	// input digest and package id no longer line up.
	forged := passReceipt(oldPkg, oldRun, "lint")
	forged.PackageID = newPkg.ID
	forged.RunID = newRun.ID
	if err := s.Submit(forged); err != ErrBindingMismatch {
		t.Fatalf("forged reuse: err = %v, want ErrBindingMismatch", err)
	}

	// The new gate stays closed: nothing from the old package counts.
	rep, err := s.Gate(newRun.ID)
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if rep.Passed {
		t.Fatal("new gate must not be satisfiable by archived results")
	}
	for id, st := range rep.Checks {
		if st != CheckPending {
			t.Fatalf("check %s state = %v, want pending", id, st)
		}
	}
	// The old run no longer belongs to the current package.
	if _, err := s.Gate(oldRun.ID); err != ErrUnknownRun {
		t.Fatalf("gate for old run: err = %v, want ErrUnknownRun", err)
	}
	if got := len(s.Archived()); got != 1 {
		t.Fatalf("archived packages = %d, want 1", got)
	}
}

// A rule (validator version) change is a new frozen package; receipts
// carrying the old version are erroneous reuse.
func TestErroneousReuseAfterRuleChange(t *testing.T) {
	s := NewScheduler()
	oldPkg := mustFreeze(t, s, "sha256:input-v1", specs())
	oldRun := mustRun(t, s)
	if err := s.Submit(passReceipt(oldPkg, oldRun, "lint")); err != nil {
		t.Fatalf("Submit lint: %v", err)
	}

	changed := specs()
	changed[1].Version = "2.2.0" // unit validator rules bumped
	newPkg := mustFreeze(t, s, "sha256:input-v1", changed)
	if newPkg.ID == oldPkg.ID {
		t.Fatal("rule change must produce a new frozen package")
	}
	newRun := mustRun(t, s)

	// Old-version receipt aimed at the new package: binding mismatch.
	staleVersion := passReceipt(oldPkg, oldRun, "unit")
	staleVersion.PackageID = newPkg.ID
	staleVersion.RunID = newRun.ID
	if err := s.Submit(staleVersion); err != ErrBindingMismatch {
		t.Fatalf("stale validator version: err = %v, want ErrBindingMismatch", err)
	}

	// Receipts bound to the new versions are accepted; gate still needs
	// every check of the same frozen package.
	for _, id := range []string{"unit", "secscan", "lint"} {
		if err := s.Submit(passReceipt(newPkg, newRun, id)); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
	}
	rep, _ := s.Gate(newRun.ID)
	if !rep.Passed {
		t.Fatalf("gate should pass with fresh receipts, states: %v", rep.Checks)
	}
}

// Receipts from different runs of the same package do not mix: the gate
// is evaluated per run.
func TestRunsDoNotMix(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run1 := mustRun(t, s)
	run2 := mustRun(t, s)

	for _, id := range []string{"lint", "unit"} {
		if err := s.Submit(passReceipt(pkg, run1, id)); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
	}
	if err := s.Submit(passReceipt(pkg, run2, "secscan")); err != nil {
		t.Fatalf("Submit secscan: %v", err)
	}
	rep1, _ := s.Gate(run1.ID)
	rep2, _ := s.Gate(run2.ID)
	if rep1.Passed || rep2.Passed {
		t.Fatal("receipts from different runs must not combine to open a gate")
	}
}

func TestDuplicateReceipt(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run := mustRun(t, s)
	if err := s.Submit(passReceipt(pkg, run, "lint")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := s.Submit(passReceipt(pkg, run, "lint")); err != ErrDuplicateReceipt {
		t.Fatalf("duplicate submit: err = %v, want ErrDuplicateReceipt", err)
	}
}

func TestFreezeIsOrderIndependent(t *testing.T) {
	reversed := []CheckSpec{specs()[2], specs()[1], specs()[0]}
	a, err := Freeze("sha256:input-v1", specs())
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	b, err := Freeze("sha256:input-v1", reversed)
	if err != nil {
		t.Fatalf("Freeze reversed: %v", err)
	}
	if a.ID != b.ID {
		t.Fatal("frozen package id must not depend on check declaration order")
	}
}

func TestUnknownPackageAndCheck(t *testing.T) {
	s := NewScheduler()
	pkg := mustFreeze(t, s, "sha256:input-v1", specs())
	run := mustRun(t, s)

	r := passReceipt(pkg, run, "lint")
	r.PackageID = "pkg-does-not-exist"
	if err := s.Submit(r); err != ErrUnknownPackage {
		t.Fatalf("unknown package: err = %v, want ErrUnknownPackage", err)
	}
	r = passReceipt(pkg, run, "lint")
	r.CheckID = "nope"
	if err := s.Submit(r); err != ErrUnknownCheck {
		t.Fatalf("unknown check: err = %v, want ErrUnknownCheck", err)
	}
	r = passReceipt(pkg, run, "lint")
	r.RunID = "run-999"
	if err := s.Submit(r); err != ErrUnknownRun {
		t.Fatalf("unknown run: err = %v, want ErrUnknownRun", err)
	}
}
