package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func weakSourceAndArgsCallback(in ActionInputs) (string, error) {
	sum := sha256.Sum256([]byte(in.SourceRoot + "\x00" + strings.Join(in.Args, "\x00")))
	return hex.EncodeToString(sum[:]), nil
}

func TestAuditFindsOmittedFields(t *testing.T) {
	report, err := AuditKeyCallback(weakSourceAndArgsCallback, baseInputs(), testHostKey, nil)
	if err != nil {
		t.Fatalf("AuditKeyCallback: %v", err)
	}
	if report.Clean() {
		t.Fatal("weak callback was reported clean")
	}
	if report.MinDifferingCount != 1 {
		t.Fatalf("shortest collision differs in %d fields, want 1", report.MinDifferingCount)
	}

	wantMissing := []Field{
		FieldWorkDir,
		FieldEnvironment,
		FieldSecretEnvironment,
		FieldToolchain,
		FieldDependencies,
		FieldPlatform,
		FieldSecurityPolicy,
	}
	for _, field := range wantMissing {
		if !containsField(report.MissingFields, field) {
			t.Errorf("missing field %q was not reported; report: %v", field, report.MissingFields)
		}
	}
	for _, field := range []Field{FieldSourceRoot, FieldCommandArgs} {
		if containsField(report.MissingFields, field) {
			t.Errorf("covered field %q was reported missing", field)
		}
	}

	for _, collision := range report.Collisions {
		if collision.CallbackKey == "" {
			t.Error("collision has an empty callback key")
		}
		if collision.LeftKey == collision.RightKey {
			t.Error("collision has equal canonical action keys")
		}
		if collision.LeftSemantics == collision.RightSemantics {
			t.Error("collision has equal output security semantics")
		}
		if collision.DifferingFieldCount != 1 {
			t.Errorf("collision differs in %d fields, want 1", collision.DifferingFieldCount)
		}
	}
}

func TestAuditStrongCallbackIsClean(t *testing.T) {
	secretKey := testHostKey
	callback := func(in ActionInputs) (string, error) {
		key, err := in.Key(secretKey)
		return key.Hex(), err
	}
	report, err := AuditKeyCallback(callback, baseInputs(), secretKey, nil)
	if err != nil {
		t.Fatalf("AuditKeyCallback: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("canonical callback reported collisions: %+v", report.Collisions)
	}
	if len(report.MissingFields) != 0 {
		t.Fatalf("clean callback has missing fields: %v", report.MissingFields)
	}
	if report.EvaluatedStates == 0 {
		t.Fatal("audit did not evaluate any state")
	}
}

func TestAuditFindsSingleIgnoredPolicyField(t *testing.T) {
	secretKey := testHostKey
	callback := func(in ActionInputs) (string, error) {
		in.SecurityPolicyDigest = "ignored"
		key, err := in.Key(secretKey)
		return key.Hex(), err
	}
	report, err := AuditKeyCallback(callback, baseInputs(), secretKey, nil)
	if err != nil {
		t.Fatalf("AuditKeyCallback: %v", err)
	}
	if len(report.MissingFields) != 1 || report.MissingFields[0] != FieldSecurityPolicy {
		t.Fatalf("missing fields = %v, want [%s]", report.MissingFields, FieldSecurityPolicy)
	}
	if report.MinDifferingCount != 1 {
		t.Fatalf("min differing count = %d, want 1", report.MinDifferingCount)
	}
	if field, ok := report.MissingField(); !ok || field != FieldSecurityPolicy {
		t.Fatalf("MissingField = %q, %v", field, ok)
	}
}

func TestAuditDoesNotReportIdenticalSemantics(t *testing.T) {
	candidates := []FieldCandidate{
		{
			Field:      FieldSourceRoot,
			ValueA:     "/a",
			ValueB:     "/b",
			SemanticsA: "same",
			SemanticsB: "same",
		},
	}
	_, err := AuditKeyCallback(weakSourceAndArgsCallback, baseInputs(), testHostKey, candidates)
	if err == nil {
		t.Fatal("candidate values with identical output semantics were accepted")
	}
}

func TestAuditRejectsDuplicateFields(t *testing.T) {
	candidates := []FieldCandidate{
		{Field: FieldSourceRoot, ValueA: "/a", ValueB: "/b", SemanticsA: "a", SemanticsB: "b"},
		{Field: FieldSourceRoot, ValueA: "/c", ValueB: "/d", SemanticsA: "c", SemanticsB: "d"},
	}
	_, err := AuditKeyCallback(weakSourceAndArgsCallback, baseInputs(), testHostKey, candidates)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
}

func TestAuditWithSecretBaselineRequiresHostKey(t *testing.T) {
	baseline := baseInputs()
	baseline.Env = append(baseline.Env, EnvVar{Name: "TOKEN", Value: "secret", Secret: true})
	_, err := AuditKeyCallback(weakSourceAndArgsCallback, baseline, nil, nil)
	if !errors.Is(err, ErrSecretKeyRequired) {
		t.Fatalf("expected ErrSecretKeyRequired, got %v", err)
	}
}

func TestAuditReportDoesNotExposeSecretValues(t *testing.T) {
	baseline := baseInputs()
	baseline.Env = append(baseline.Env, EnvVar{Name: "TOKEN", Value: "do-not-print-this", Secret: true})
	report, err := AuditKeyCallback(weakSourceAndArgsCallback, baseline, testHostKey, nil)
	if err != nil {
		t.Fatalf("AuditKeyCallback: %v", err)
	}
	rendered := fmt.Sprintf("%+v", report)
	if strings.Contains(rendered, "do-not-print-this") {
		t.Fatal("audit report exposed a secret environment value")
	}
}

func TestAuditCustomCandidatesFindOnlyProvidedField(t *testing.T) {
	candidates := []FieldCandidate{
		{
			Field:      FieldWorkDir,
			ValueA:     "/work/a",
			ValueB:     "/work/b",
			SemanticsA: "work-a",
			SemanticsB: "work-b",
		},
	}
	// This callback ignores working directory but covers source and args.
	report, err := AuditKeyCallback(weakSourceAndArgsCallback, baseInputs(), testHostKey, candidates)
	if err != nil {
		t.Fatalf("AuditKeyCallback: %v", err)
	}
	if len(report.MissingFields) != 1 || report.MissingFields[0] != FieldWorkDir {
		t.Fatalf("missing fields = %v, want [%s]", report.MissingFields, FieldWorkDir)
	}
}

func containsField(fields []Field, want Field) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
