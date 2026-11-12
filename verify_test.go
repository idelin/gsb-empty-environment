package netsec

import (
	"encoding/json"
	"errors"
	"testing"
)

const (
	testTrustedBuilder = "https://build.example.com/slsa"
	testBuildType      = "https://build.example.com/slsa/v1"
	testSourceCommit   = "0123456789abcdef0123456789abcdef01234567"
	testRepository     = "github.com/example/project"
)

func testPolicy() Policy {
	return Policy{
		TrustedBuilders: map[string]string{
			testTrustedBuilder: testBuildType,
		},
		AllowedExternalParameters: []string{"source", "branch"},
		SourceCommit:              testSourceCommit,
		SourceRepository:          testRepository,
	}
}

func validStatement() *Statement {
	return &Statement{
		Type:          StatementType,
		PredicateType: PredicateType,
		Predicate: Predicate{
			BuildDefinition: BuildDefinition{
				BuildType:          testBuildType,
				ExternalParameters: json.RawMessage(`{"source":{},"branch":"main"}`),
				ResolvedDependencies: []ResourceDescriptor{{
					URI:    "git+https://github.com/example/project@refs/heads/main",
					Digest: DigestSet{"gitCommit": testSourceCommit},
				}},
			},
			RunDetails: RunDetails{
				Builder: Builder{ID: testTrustedBuilder},
			},
		},
	}
}

func TestVerifyAcceptsTrustedProvenance(t *testing.T) {
	if err := Verify(validStatement(), testPolicy()); err != nil {
		t.Fatalf("Verify() rejected a valid statement: %v", err)
	}
}

func TestVerifyRejectsUnregisteredExternalParameter(t *testing.T) {
	statement := validStatement()
	statement.Predicate.BuildDefinition.ExternalParameters = json.RawMessage(
		`{"source":{},"branch":"main","unexpected":"attacker-controlled"}`,
	)

	err := Verify(statement, testPolicy())
	if !errors.Is(err, ErrUnregisteredExternalParameter) {
		t.Fatalf("Verify() error = %v, want ErrUnregisteredExternalParameter", err)
	}
}

func TestVerifyRequiresExactBuilderID(t *testing.T) {
	policy := testPolicy()
	for _, builderID := range []string{
		testTrustedBuilder + "-evil",
		testTrustedBuilder + ".evil",
		testTrustedBuilder + "/",
		"https://build.example.com/slsa-other",
	} {
		t.Run(builderID, func(t *testing.T) {
			statement := validStatement()
			statement.Predicate.RunDetails.Builder.ID = builderID

			err := Verify(statement, policy)
			if !errors.Is(err, ErrUntrustedBuilder) {
				t.Fatalf("Verify() error = %v, want ErrUntrustedBuilder", err)
			}
		})
	}
}

func TestVerifyRejectsBuildTypeMismatch(t *testing.T) {
	statement := validStatement()
	statement.Predicate.BuildDefinition.BuildType = testBuildType + "/untrusted"

	err := Verify(statement, testPolicy())
	if !errors.Is(err, ErrBuildTypeMismatch) {
		t.Fatalf("Verify() error = %v, want ErrBuildTypeMismatch", err)
	}
}

func TestVerifyRejectsPresenceOfInternalParameters(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"empty object": json.RawMessage(`{}`),
		"null":         json.RawMessage(`null`),
		"populated":    json.RawMessage(`{"builderSecret":"value"}`),
	} {
		t.Run(name, func(t *testing.T) {
			statement := validStatement()
			statement.Predicate.BuildDefinition.InternalParameters = raw

			err := Verify(statement, testPolicy())
			if !errors.Is(err, ErrInternalParametersPresent) {
				t.Fatalf("Verify() error = %v, want ErrInternalParametersPresent", err)
			}
		})
	}
}

func TestVerifyRejectsMissingOrMismatchedSourceDigest(t *testing.T) {
	tests := map[string][]ResourceDescriptor{
		"missing digest": {{
			URI: "git+https://github.com/example/project@refs/heads/main",
		}},
		"empty digest": {{
			URI:    "git+https://github.com/example/project@refs/heads/main",
			Digest: DigestSet{},
		}},
		"wrong commit": {{
			URI:    "git+https://github.com/example/project@refs/heads/main",
			Digest: DigestSet{"gitCommit": "ffffffffffffffffffffffffffffffffffffffff"},
		}},
		"no dependencies": {},
	}

	for name, dependencies := range tests {
		t.Run(name, func(t *testing.T) {
			statement := validStatement()
			statement.Predicate.BuildDefinition.ResolvedDependencies = dependencies

			err := Verify(statement, testPolicy())
			if !errors.Is(err, ErrSourceCommitMissing) {
				t.Fatalf("Verify() error = %v, want ErrSourceCommitMissing", err)
			}
		})
	}
}

func TestVerifyKeepsSourceCommitBoundToRepository(t *testing.T) {
	statement := validStatement()
	statement.Predicate.BuildDefinition.ResolvedDependencies[0].URI =
		"https://github.com/example/unrelated@refs/heads/main"

	err := Verify(statement, testPolicy())
	if !errors.Is(err, ErrSourceCommitMissing) {
		t.Fatalf("Verify() error = %v, want ErrSourceCommitMissing", err)
	}
}

func TestVerifyDoesNotAcceptRepositoryLookalikeHost(t *testing.T) {
	statement := validStatement()
	statement.Predicate.BuildDefinition.ResolvedDependencies[0].URI =
		"https://evil.example/github.com/example/project@refs/heads/main"

	err := Verify(statement, testPolicy())
	if !errors.Is(err, ErrSourceCommitMissing) {
		t.Fatalf("Verify() error = %v, want ErrSourceCommitMissing", err)
	}
}

func TestVerifyJSON(t *testing.T) {
	valid, err := json.Marshal(validStatement())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := VerifyJSON(valid, testPolicy()); err != nil {
		t.Fatalf("VerifyJSON() rejected valid JSON: %v", err)
	}

	attacked := []byte(`{
		"_type": "https://in-toto.io/Statement/v1",
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": {
			"buildDefinition": {
				"buildType": "https://build.example.com/slsa/v1",
				"externalParameters": {"source": {}, "extra": true},
				"resolvedDependencies": [
					{"uri": "git+https://github.com/example/project", "digest": {"gitCommit": "` + testSourceCommit + `"}}
				]
			},
			"runDetails": {"builder": {"id": "` + testTrustedBuilder + `"}}
		}
	}`)
	if err := VerifyJSON(attacked, testPolicy()); !errors.Is(err, ErrUnregisteredExternalParameter) {
		t.Fatalf("VerifyJSON() error = %v, want ErrUnregisteredExternalParameter", err)
	}
}

func TestParseStatementRejectsTrailingJSON(t *testing.T) {
	if _, err := ParseStatement([]byte(`{} {}`)); !errors.Is(err, ErrInvalidStatement) {
		t.Fatalf("ParseStatement() error = %v, want ErrInvalidStatement", err)
	}
}

func TestVerifierConvenienceAPI(t *testing.T) {
	verifier := NewVerifier(testPolicy())
	if err := verifier.Verify(validStatement()); err != nil {
		t.Fatalf("Verifier.Verify() error = %v", err)
	}
}
