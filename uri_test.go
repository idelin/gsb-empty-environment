package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalJobURIAndParseRoundTrip(t *testing.T) {
	scope := Scope{Queue: "queue-a", Submitter: "alice", JobID: "job-123"}
	uri, err := CanonicalJobURI(scope, "report final.pdf")
	if err != nil {
		t.Fatalf("CanonicalJobURI: %v", err)
	}
	if uri != "job:///queue-a/alice/job-123/report%20final.pdf" {
		t.Fatalf("URI = %q", uri)
	}
	gotScope, gotDocument, err := ParseJobURI(uri)
	if err != nil {
		t.Fatalf("ParseJobURI: %v", err)
	}
	if gotScope != scope || gotDocument != "report final.pdf" {
		t.Fatalf("round trip = %+v %q", gotScope, gotDocument)
	}
}

func TestValidateJobURIRejectsCrossScope(t *testing.T) {
	alice := Scope{Queue: "queue-a", Submitter: "alice", JobID: "job-a"}
	bob := Scope{Queue: "queue-b", Submitter: "bob", JobID: "job-b"}
	bobURI, err := CanonicalJobURI(bob, "bob.txt")
	if err != nil {
		t.Fatalf("CanonicalJobURI: %v", err)
	}
	if err := ValidateJobURI(bobURI, alice, "alice.txt"); !errors.Is(err, ErrURIOutsideJobStorage) {
		t.Fatalf("error = %v, want ErrURIOutsideJobStorage", err)
	}
	aliceURI, err := CanonicalJobURI(alice, "alice.txt")
	if err != nil {
		t.Fatalf("CanonicalJobURI: %v", err)
	}
	if err := ValidateJobURI(aliceURI, alice, "other.txt"); !errors.Is(err, ErrURIOutsideJobStorage) {
		t.Fatalf("error = %v, want ErrURIOutsideJobStorage", err)
	}
	if err := ValidateJobURI(aliceURI, alice, "alice.txt"); err != nil {
		t.Fatalf("own URI rejected: %v", err)
	}
}

func TestJobVerifyRejectsTamperedURI(t *testing.T) {
	job, err := BindJob(JobSpec{
		ID:           "job-a",
		Queue:        "queue-a",
		Submitter:    "alice",
		DocumentName: "alice.txt",
		Digest:       strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("BindJob: %v", err)
	}
	bobURI, err := CanonicalJobURI(Scope{Queue: "queue-b", Submitter: "bob", JobID: "job-b"}, "bob.txt")
	if err != nil {
		t.Fatalf("CanonicalJobURI: %v", err)
	}
	job.DocumentURI = bobURI
	if err := job.Verify(); !errors.Is(err, ErrURIOutsideJobStorage) {
		t.Fatalf("Verify error = %v, want ErrURIOutsideJobStorage", err)
	}
}

func TestParseJobURIRejectsAmbiguousForms(t *testing.T) {
	bad := []string{
		"",
		"job://queue/user/job/doc",
		"job:///queue/user/job",
		"job:///queue/user/job/doc/extra",
		"job:///queue/user/job/",
		"job:///queue/user/job/doc?x=1",
		"job:///queue/user/job/doc#frag",
		"JOB:///queue/user/job/doc",
		"job:///queue/user/job/doc%2Fother",
		"job:///queue/user/job/..%2Fother",
		"job:///queue/user/job/%2e%2e",
		"job:///queue/user/job/a%5Cb",
		"job:///queue/user/job/doc%00",
		"job://user@queue/user/job/doc",
		"job:///../user/job/doc",
		"job:///queue/../job/doc",
	}
	for _, raw := range bad {
		if _, _, err := ParseJobURI(raw); err == nil {
			t.Errorf("ParseJobURI(%q) succeeded", raw)
		}
	}
}

func TestUnsafeDocumentNamesRejected(t *testing.T) {
	scope := Scope{Queue: "q", Submitter: "u", JobID: "j"}
	bad := []string{
		"",
		"../victim.txt",
		"a/b.txt",
		`a\b.txt`,
		".hidden",
		"..",
		"name.",
		"name ",
		"a\x00b",
		"a\u202Eb.txt",
		strings.Repeat("a", maxDocumentLen+1),
	}
	for _, name := range bad {
		if _, err := CanonicalJobURI(scope, name); err == nil {
			t.Errorf("CanonicalJobURI accepted %q", name)
		}
	}
}
