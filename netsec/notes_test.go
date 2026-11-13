package netsec

import "testing"

const (
	targetOne    = "0123456789abcdef0123456789abcdef01234567"
	targetTwo    = "fedcba9876543210fedcba9876543210fedcba98"
	authorized   = "alice@example.com"
	unauthorized = "mallory@example.com"
)

func approvalBlob(t *testing.T, email string) ([]byte, string) {
	t.Helper()
	content := []byte("Approved-by: " + email + "\n")
	return content, BlobID(content)
}

func baseInput(t *testing.T) (Input, string) {
	t.Helper()
	content, blobID := approvalBlob(t, authorized)
	in := Input{
		Blobs:      map[string][]byte{blobID: content},
		Authorized: map[string]bool{authorized: true},
	}
	return in, blobID
}

// An authorized signer adds a brand new approval note: it is trusted.
func TestValidateAuthorizedNewNote(t *testing.T) {
	in, blobID := baseInput(t)
	path := targetOne[:2] + "/" + targetOne[2:]
	in.Commits = []Commit{
		{
			ID:       "1111111111111111111111111111111111111111",
			Signer:   authorized,
			Verified: true,
			Paths:    map[string]string{path: blobID},
		},
	}

	report, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("unexpected violations: %+v", report.Violations)
	}
	approval, ok := report.Approvals[targetOne]
	if !ok {
		t.Fatalf("target %s was not approved; report=%+v", targetOne, report)
	}
	if approval.Path != path || approval.BlobID != blobID {
		t.Fatalf("approval mismatch: %+v", approval)
	}
	if approval.ApprovedBy != authorized {
		t.Fatalf("approved-by = %q, want %q", approval.ApprovedBy, authorized)
	}
	if approval.IntroducedBy != in.Commits[0].ID {
		t.Fatalf("introducer = %q, want %q", approval.IntroducedBy, in.Commits[0].ID)
	}
}

// Two fan-out spellings of the same target conflict and void every note for
// that target, including a note that was authorized when first introduced.
func TestValidateLayeredPathConflict(t *testing.T) {
	in, blobID := baseInput(t)
	firstSpelling := targetOne[:2] + "/" + targetOne[2:]
	secondSpelling := targetOne[:3] + "/" + targetOne[3:]

	in.Commits = []Commit{
		{
			ID:       "1111111111111111111111111111111111111111",
			Signer:   authorized,
			Verified: true,
			Paths:    map[string]string{firstSpelling: blobID},
		},
		{
			ID:       "2222222222222222222222222222222222222222",
			Signer:   unauthorized,
			Verified: false,
			Paths: map[string]string{
				firstSpelling:  blobID,
				secondSpelling: blobID,
			},
		},
	}

	report, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if _, ok := report.Approvals[targetOne]; ok {
		t.Fatalf("conflicted target must not be approved: %+v", report.Approvals)
	}
	if len(report.Violations) != 1 || report.Violations[0].Code != ViolationPathConflict {
		t.Fatalf("want one path_conflict violation, got %+v", report.Violations)
	}
	if report.Violations[0].Target != targetOne {
		t.Fatalf("violation target = %q, want %q", report.Violations[0].Target, targetOne)
	}
}

// An unsigned commit writes the note; a later authorized commit merely keeps
// the same blob in place. Retention is not authorship, so the note stays
// untrusted.
func TestValidateUnauthorizedWriteLaterRetained(t *testing.T) {
	in, blobID := baseInput(t)
	path := targetOne[:2] + "/" + targetOne[2:]

	in.Commits = []Commit{
		{
			ID:       "1111111111111111111111111111111111111111",
			Signer:   unauthorized,
			Verified: false,
			Paths:    map[string]string{path: blobID},
		},
		{
			ID:       "2222222222222222222222222222222222222222",
			Signer:   authorized,
			Verified: true,
			Paths:    map[string]string{path: blobID},
		},
	}

	report, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if len(report.Approvals) != 0 {
		t.Fatalf("retained unauthorized note must not be approved: %+v", report.Approvals)
	}
	if len(report.Violations) != 1 || report.Violations[0].Code != ViolationUntrustedHistory {
		t.Fatalf("want one untrusted_introduction violation, got %+v", report.Violations)
	}
}

// Paths containing uppercase hexadecimal never normalize to the required
// lowercase 40-character target and are rejected, while a valid sibling note
// is still approved.
func TestValidateUppercaseHexPath(t *testing.T) {
	in, blobID := baseInput(t)
	upper := "ABCDEF6789ABCDEF0123456789ABCDEF01234567"
	upperPath := upper[:2] + "/" + upper[2:]
	goodPath := targetTwo[:2] + "/" + targetTwo[2:]

	in.Commits = []Commit{
		{
			ID:       "1111111111111111111111111111111111111111",
			Signer:   authorized,
			Verified: true,
			Paths: map[string]string{
				upperPath: blobID,
				goodPath:  blobID,
			},
		},
	}

	report, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if _, ok := report.Approvals[targetTwo]; !ok {
		t.Fatalf("valid lowercase path should still be approved: %+v", report)
	}
	if _, ok := report.Approvals[upper]; ok {
		t.Fatalf("uppercase path must never be approved")
	}
	var invalid []Violation
	for _, violation := range report.Violations {
		if violation.Code == ViolationInvalidPath {
			invalid = append(invalid, violation)
		}
	}
	if len(invalid) != 1 || invalid[0].Path != upperPath {
		t.Fatalf("want one invalid_path violation for %q, got %+v", upperPath, report.Violations)
	}
}

// Only the first appearance of the current blob content at the path counts:
// an authorized later rewrite is trusted, while reverting to content first
// introduced by an unauthorized commit remains untrusted.
func TestValidateFirstIntroductionSemantics(t *testing.T) {
	goodContent, goodID := approvalBlob(t, authorized)
	badContent, badID := approvalBlob(t, unauthorized)
	path := targetOne[:2] + "/" + targetOne[2:]

	in := Input{
		Blobs: map[string][]byte{
			goodID: goodContent,
			badID:  badContent,
		},
		Authorized: map[string]bool{authorized: true},
		Commits: []Commit{
			{
				ID:       "1111111111111111111111111111111111111111",
				Signer:   unauthorized,
				Verified: false,
				Paths:    map[string]string{path: badID},
			},
			{
				ID:       "2222222222222222222222222222222222222222",
				Signer:   authorized,
				Verified: true,
				Paths:    map[string]string{path: goodID},
			},
		},
	}

	report, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	approval, ok := report.Approvals[targetOne]
	if !ok {
		t.Fatalf("content first introduced by authorized commit should be approved: %+v", report)
	}
	if approval.ApprovedBy != authorized || approval.IntroducedBy != in.Commits[1].ID {
		t.Fatalf("approval should name authorized introducer, got %+v", approval)
	}

	// Revert to the unauthorized blob; its first introduction is still commit one.
	in.Commits = append(in.Commits, Commit{
		ID:       "3333333333333333333333333333333333333333",
		Signer:   authorized,
		Verified: true,
		Paths:    map[string]string{path: badID},
	})
	report, err = Validate(in)
	if err != nil {
		t.Fatalf("revalidation returned error: %v", err)
	}
	if len(report.Approvals) != 0 {
		t.Fatalf("reverted content must not be approved: %+v", report.Approvals)
	}
	if len(report.Violations) != 1 || report.Violations[0].Code != ViolationUntrustedHistory {
		t.Fatalf("want untrusted introduction after revert, got %+v", report.Violations)
	}
}

func TestValidateSignatureAndAuthorizationChecks(t *testing.T) {
	path := targetOne[:2] + "/" + targetOne[2:]

	tests := []struct {
		name       string
		signer     string
		verified   bool
		authorized map[string]bool
		want       ViolationCode
	}{
		{
			name:       "verified but unauthorized signer",
			signer:     unauthorized,
			verified:   true,
			authorized: map[string]bool{authorized: true},
			want:       ViolationUntrustedHistory,
		},
		{
			name:       "authorized but unverified signature",
			signer:     authorized,
			verified:   false,
			authorized: map[string]bool{authorized: true},
			want:       ViolationUntrustedHistory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, id := approvalBlob(t, authorized)
			in := Input{
				Blobs:      map[string][]byte{id: content},
				Authorized: tt.authorized,
				Commits: []Commit{
					{
						ID:       "1111111111111111111111111111111111111111",
						Signer:   tt.signer,
						Verified: tt.verified,
						Paths:    map[string]string{path: id},
					},
				},
			}

			report, err := Validate(in)
			if err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
			if len(report.Approvals) != 0 {
				t.Fatalf("expected no approvals, got %+v", report.Approvals)
			}
			if len(report.Violations) != 1 || report.Violations[0].Code != tt.want {
				t.Fatalf("want %s, got %+v", tt.want, report.Violations)
			}
		})
	}
}

func TestValidateBadApprovalLine(t *testing.T) {
	path := targetOne[:2] + "/" + targetOne[2:]
	cases := map[string][]byte{
		"missing prefix":     []byte("alice@example.com\n"),
		"empty email":        []byte("Approved-by: \n"),
		"angle bracket form": []byte("Approved-by: Alice <alice@example.com>\n"),
		"not an email":       []byte("Approved-by: alice-on-vacation\n"),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			id := BlobID(content)
			in := Input{
				Blobs:      map[string][]byte{id: content},
				Authorized: map[string]bool{authorized: true},
				Commits: []Commit{
					{
						ID:       "1111111111111111111111111111111111111111",
						Signer:   authorized,
						Verified: true,
						Paths:    map[string]string{path: id},
					},
				},
			}

			report, err := Validate(in)
			if err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
			if len(report.Approvals) != 0 {
				t.Fatalf("malformed blob must not be approved: %+v", report.Approvals)
			}
			if len(report.Violations) != 1 || report.Violations[0].Code != ViolationBadApprovalLine {
				t.Fatalf("want bad_approval_line, got %+v", report.Violations)
			}
		})
	}
}

func TestValidateInputErrors(t *testing.T) {
	t.Run("empty commit chain", func(t *testing.T) {
		if _, err := Validate(Input{}); err == nil {
			t.Fatal("expected error for empty commit chain")
		}
	})

	t.Run("blob content mismatch", func(t *testing.T) {
		_, blobID := approvalBlob(t, authorized)
		in := Input{
			Blobs: map[string][]byte{blobID: []byte("tampered")},
			Commits: []Commit{
				{ID: "1111111111111111111111111111111111111111"},
			},
		}
		if _, err := Validate(in); err == nil {
			t.Fatal("expected error for blob whose ID does not hash its content")
		}
	})

	t.Run("missing referenced blob", func(t *testing.T) {
		_, blobID := approvalBlob(t, authorized)
		path := targetOne[:2] + "/" + targetOne[2:]
		in := Input{
			Authorized: map[string]bool{authorized: true},
			Commits: []Commit{
				{
					ID:    "1111111111111111111111111111111111111111",
					Paths: map[string]string{path: blobID},
				},
			},
		}
		report, err := Validate(in)
		if err != nil {
			t.Fatalf("missing blob is a violation, not an error: %v", err)
		}
		if len(report.Violations) != 1 || report.Violations[0].Code != ViolationBlobMissing {
			t.Fatalf("want blob_missing, got %+v", report.Violations)
		}
	})
}

func TestBlobIDMatchesGit(t *testing.T) {
	// "hello\n" -> blob 6 bytes; compare against the well-known git object ID
	// ce013625030ba8dba906f756967f9e9ca394464a.
	content := []byte("hello\n")
	if got, want := BlobID(content), "ce013625030ba8dba906f756967f9e9ca394464a"; got != want {
		t.Fatalf("BlobID = %q, want %q", got, want)
	}
}
