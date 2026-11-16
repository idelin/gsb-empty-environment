package netsec

import (
	"strings"
	"testing"
)

const (
	targetA = "0123456789abcdef0123456789abcdef01234567"
	targetB = "89abcdef0123456789abcdef0123456789abcdef"
	targetC = "fedcba9876543210fedcba9876543210fedcba98"
)

func splitPath(target string) string {
	return target[:2] + "/" + target[2:]
}

func approval(email string) []byte {
	return []byte("Approved-by: " + email + "\n\nReviewed-by: release-team\n")
}

func snapshot(entries ...Entry) Tree {
	tree := Tree{Entries: entries}
	for _, entry := range entries {
		tree.Paths = append(tree.Paths, entry.Path)
		if tree.Blobs == nil {
			tree.Blobs = make(map[string][]byte)
		}
		tree.Blobs[entry.Path] = entry.Content
	}
	return tree
}

func commit(signer string, verified bool, tree Tree) Commit {
	return Commit{Signer: signer, Verified: verified, Tree: tree}
}

func reasonFor(t *testing.T, result Result, target string) string {
	t.Helper()
	for _, rejection := range result.Rejected {
		if rejection.Target == target {
			return rejection.Reason
		}
	}
	t.Fatalf("no rejection for target %s: %+v", target, result)
	return ""
}

func TestHierarchicalPathConflictInvalidatesWholeTarget(t *testing.T) {
	path2 := splitPath(targetA)
	path3 := targetA[:3] + "/" + targetA[3:]
	content := approval("reviewer@example.com")

	current := snapshot(
		Entry{Path: path2, Content: content},
		Entry{Path: path3, Content: content},
		Entry{Path: splitPath(targetB), Content: approval("other@example.com")},
	)
	history := []Commit{commit("alice", true, current)}

	result := Validate(history, current, []string{"alice"})
	if len(result.Accepted) != 1 || result.Accepted[0].Target != targetB {
		t.Fatalf("only the unrelated target should be accepted: %+v", result.Accepted)
	}
	if result.IsAccepted(targetA) {
		t.Fatal("conflicting target must not be accepted")
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(result.Conflicts), result.Conflicts)
	}
	if result.Conflicts[0].Target != targetA {
		t.Fatalf("conflict target = %q, want %q", result.Conflicts[0].Target, targetA)
	}
	if len(result.Conflicts[0].Paths) != 2 {
		t.Fatalf("conflict paths = %v, want both spellings", result.Conflicts[0].Paths)
	}
	if got := reasonFor(t, result, targetA); got != ReasonConflictingPaths {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonConflictingPaths)
	}
}

func TestUppercaseTargetPathIsRejected(t *testing.T) {
	lowerPath := splitPath(targetA)
	upperPath := strings.ToUpper(lowerPath)
	content := approval("reviewer@example.com")

	current := snapshot(
		Entry{Path: lowerPath, Content: content},
		Entry{Path: upperPath, Content: content},
	)
	history := []Commit{commit("alice", true, current)}

	result := Validate(history, current, []string{"alice"})
	if !result.IsAccepted(targetA) {
		t.Fatalf("lowercase path should remain accepted: %+v", result)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("an invalid uppercase path must not create a target conflict: %+v", result.Conflicts)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1: %+v", len(result.Rejected), result.Rejected)
	}
	if result.Rejected[0].Path != upperPath || result.Rejected[0].Reason != ReasonInvalidPath {
		t.Fatalf("unexpected rejection: %+v", result.Rejected[0])
	}
}

func TestUnauthorizedIntroductionIsNotBlessedByLaterPreservation(t *testing.T) {
	path := splitPath(targetA)
	content := approval("reviewer@example.com")
	tree := snapshot(Entry{Path: path, Content: content})

	history := []Commit{
		commit("mallory", true, tree),
		commit("alice", true, tree),
	}

	result := Validate(history, tree, []string{"alice"})
	if len(result.Accepted) != 0 {
		t.Fatalf("unauthorized introduction must not be accepted: %+v", result.Accepted)
	}
	if got := reasonFor(t, result, targetA); got != ReasonUnauthorizedSigner {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonUnauthorizedSigner)
	}
}

func TestUnsignedAppendPreservedByAuthorizedCommitIsRejected(t *testing.T) {
	path := splitPath(targetA)
	original := approval("reviewer@example.com")
	appended := append(append([]byte{}, original...), []byte("Approved-by: mallory@example.com\n")...)

	history := []Commit{
		commit("alice", true, snapshot(Entry{Path: path, Content: original})),
		commit("", false, snapshot(Entry{Path: path, Content: appended})),
		commit("alice", true, snapshot(Entry{Path: path, Content: appended})),
	}
	latest := history[len(history)-1].Tree

	result := Validate(history, latest, []string{"alice"})
	if result.IsAccepted(targetA) {
		t.Fatalf("an approval appended by an unsigned commit must not be accepted: %+v", result)
	}
	if got := reasonFor(t, result, targetA); got != ReasonUnverifiedCommit {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonUnverifiedCommit)
	}
}

func TestAuthorizedCommitAddingNewNoteIsAccepted(t *testing.T) {
	path := splitPath(targetA)
	content := approval("reviewer@example.com")
	added := snapshot(Entry{Path: path, Content: content})

	history := []Commit{
		commit("alice", true, Tree{}),
		commit("alice", true, added),
	}

	result := Validate(history, added, []string{"alice"})
	if len(result.Accepted) != 1 {
		t.Fatalf("got %d accepted notes, want 1: %+v", len(result.Accepted), result)
	}
	note := result.Accepted[0]
	if note.Target != targetA || note.Path != path || note.ApprovedBy != "reviewer@example.com" {
		t.Fatalf("unexpected accepted note: %+v", note)
	}
}

func TestCurrentContentMustBeIntroducedByAuthorizedCommit(t *testing.T) {
	path := splitPath(targetA)
	first := approval("reviewer@example.com")
	second := append(approval("other@example.com"), []byte("second version\n")...)

	history := []Commit{
		commit("alice", true, snapshot(Entry{Path: path, Content: first})),
		commit("mallory", true, snapshot(Entry{Path: path, Content: second})),
	}
	latest := history[len(history)-1].Tree

	result := Validate(history, latest, []string{"alice"})
	if result.IsAccepted(targetA) {
		t.Fatalf("an unauthorized content replacement must not be accepted: %+v", result)
	}
	if got := reasonFor(t, result, targetA); got != ReasonUnauthorizedSigner {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonUnauthorizedSigner)
	}
}

func TestAuthorizedReplacementOfUnauthorizedContentIsAccepted(t *testing.T) {
	path := splitPath(targetA)
	first := approval("reviewer@example.com")
	second := append(approval("maintainer@example.com"), []byte("replacement\n")...)

	history := []Commit{
		commit("mallory", true, snapshot(Entry{Path: path, Content: first})),
		commit("alice", true, snapshot(Entry{Path: path, Content: second})),
	}
	latest := history[len(history)-1].Tree

	result := Validate(history, latest, []string{"alice"})
	if !result.IsAccepted(targetA) {
		t.Fatalf("the currently stored content was introduced by an authorized commit: %+v", result)
	}
}

func TestUnverifiedIntroducingCommitIsRejected(t *testing.T) {
	path := splitPath(targetA)
	tree := snapshot(Entry{Path: path, Content: approval("reviewer@example.com")})

	result := Validate([]Commit{commit("alice", false, tree)}, tree, []string{"alice"})
	if result.IsAccepted(targetA) {
		t.Fatal("an unverified signature must not be accepted")
	}
	if got := reasonFor(t, result, targetA); got != ReasonUnverifiedCommit {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonUnverifiedCommit)
	}
}

func TestApprovalHeader(t *testing.T) {
	path := splitPath(targetA)

	tests := []struct {
		name    string
		content string
		ok      bool
	}{
		{name: "valid", content: "Approved-by: dev@example.com\n", ok: true},
		{name: "valid CRLF", content: "Approved-by: dev@example.com\r\n\r\ncomment\r\n", ok: true},
		{name: "extra comment lines", content: "Approved-by: dev@example.com\n\nLGTM\n", ok: true},
		{name: "missing space", content: "Approved-by:dev@example.com\n", ok: false},
		{name: "wrong case", content: "approved-by: dev@example.com\n", ok: false},
		{name: "display name", content: "Approved-by: Dev <dev@example.com>\n", ok: false},
		{name: "leading space", content: "Approved-by:  dev@example.com\n", ok: false},
		{name: "trailing space", content: "Approved-by: dev@example.com \n", ok: false},
		{name: "empty mailbox", content: "Approved-by: \n", ok: false},
		{name: "second line", content: "comment\nApproved-by: dev@example.com\n", ok: false},
		{name: "empty blob", content: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := snapshot(Entry{Path: path, Content: []byte(tt.content)})
			result := Validate([]Commit{commit("alice", true, tree)}, tree, []string{"alice"})
			if got := result.IsAccepted(targetA); got != tt.ok {
				t.Fatalf("accepted = %v, want %v; result: %+v", got, tt.ok, result)
			}
			if !tt.ok {
				if got := reasonFor(t, result, targetA); got != ReasonInvalidApproval {
					t.Fatalf("rejection reason = %q, want %q", got, ReasonInvalidApproval)
				}
			}
		})
	}
}

func TestTargetID(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{path: targetA, want: targetA, ok: true},
		{path: splitPath(targetA), want: targetA, ok: true},
		{path: "//" + targetA[:10] + "/" + targetA[10:] + "/", want: targetA, ok: true},
		{path: strings.ToUpper(targetA), ok: false},
		{path: targetA[:39], ok: false},
		{path: targetA + "0", ok: false},
		{path: "g" + targetA[1:], ok: false},
		{path: targetA[:5] + "-" + targetA[6:], ok: false},
	}

	for _, tt := range tests {
		got, ok := TargetID(tt.path)
		if ok != tt.ok || got != tt.want {
			t.Errorf("TargetID(%q) = (%q, %v), want (%q, %v)", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

func TestMissingIntroductionAndMissingBlob(t *testing.T) {
	path := splitPath(targetA)
	current := Tree{Paths: []string{path}}

	result := Validate([]Commit{commit("alice", true, Tree{})}, current, []string{"alice"})
	if got := reasonFor(t, result, targetA); got != ReasonMissingBlob {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonMissingBlob)
	}

	result = Validate(nil, snapshot(Entry{Path: path, Content: approval("dev@example.com")}), []string{"alice"})
	if got := reasonFor(t, result, targetA); got != ReasonNoIntroduction {
		t.Fatalf("rejection reason = %q, want %q", got, ReasonNoIntroduction)
	}
}

func TestConflictIsScopedToTarget(t *testing.T) {
	first := targetA[:2] + "/" + targetA[2:]
	second := targetA[:4] + "/" + targetA[4:]
	third := splitPath(targetC)
	contentA := approval("a@example.com")
	contentC := approval("c@example.com")

	current := snapshot(
		Entry{Path: first, Content: contentA},
		Entry{Path: second, Content: contentA},
		Entry{Path: third, Content: contentC},
	)
	result := Validate([]Commit{commit("alice", true, current)}, current, []string{"alice"})

	if result.IsAccepted(targetA) {
		t.Fatal("conflicting target A must not be accepted")
	}
	if !result.IsAccepted(targetC) {
		t.Fatal("target C must remain accepted")
	}
}

func TestLatestTreeFallsBackToLastCommit(t *testing.T) {
	path := splitPath(targetA)
	tree := snapshot(Entry{Path: path, Content: approval("dev@example.com")})

	result := Validate([]Commit{commit("alice", true, tree)}, Tree{}, []string{"alice"})
	if !result.IsAccepted(targetA) {
		t.Fatalf("last commit tree should be used when latest is empty: %+v", result)
	}
}
