package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newMemoryTestService(t *testing.T) (*Service, *MemoryStorage) {
	t.Helper()
	store := NewMemoryStorage()
	service, err := NewService(store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, store
}

func newFileTestService(t *testing.T) (*Service, *FileStorage) {
	t.Helper()
	store, err := NewFileStorage(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, store
}

func submitJob(t *testing.T, service *Service, submitter, queue, document, content string, attributes map[string]string) Job {
	t.Helper()
	job, err := service.Submit(context.Background(), submitter, SubmitRequest{
		Queue:        queue,
		DocumentName: document,
		Content:      []byte(content),
		Attributes:   attributes,
	})
	if err != nil {
		t.Fatalf("Submit(%s/%s): %v", submitter, queue, err)
	}
	return job
}

func TestLegitimatePrintReturnsOnlyOwnDocument(t *testing.T) {
	service, _ := newMemoryTestService(t)
	aliceJob := submitJob(t, service, "alice", "q1", "alice.txt", "alice-private-document", nil)
	submitJob(t, service, "bob", "q1", "bob.txt", "bob-secret-document", nil)

	result, err := service.Print(context.Background(), "alice", aliceJob.ID, map[string]string{
		AttrCopies: "2",
		AttrMedia:  "iso_a4_210x297mm",
		AttrSides:  "two-sided-long-edge",
		AttrTitle:  "Alice job",
	})
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	if string(result.Document) != "alice-private-document" {
		t.Fatalf("printed wrong bytes %q", result.Document)
	}
	if strings.Contains(string(result.Document), "bob") {
		t.Fatal("printed another user's document")
	}
	if result.Job.ID != aliceJob.ID || result.Job.DocumentURI != aliceJob.DocumentURI {
		t.Fatalf("result job was not the original binding: %+v", result.Job)
	}
	if got, _ := result.Attributes.Get(AttrCopies); got != "2" {
		t.Fatalf("copies attribute not applied: %q", got)
	}
	if result.Digest != aliceJob.Digest {
		t.Fatalf("digest = %s, want %s", result.Digest, aliceJob.Digest)
	}
}

func TestUnicodeIdentifiersAndDocumentName(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "张三", "打印队列", "报告 2026.pdf", "中文内容", nil)
	bobJob := submitJob(t, service, "李四", "其他队列", "秘密.pdf", "他人内容", nil)

	result, err := service.Print(context.Background(), "张三", job.ID, nil)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	if string(result.Document) != "中文内容" {
		t.Fatalf("printed %q", result.Document)
	}
	if _, err := service.PrintByAttributes(context.Background(), "张三", map[string]string{
		AttrJobID:  job.ID,
		AttrJobURI: bobJob.DocumentURI,
	}); err == nil {
		t.Fatal("Unicode job accepted another job's URI")
	}
}

func TestCrossQueueReferenceSameUserRejected(t *testing.T) {
	service, _ := newMemoryTestService(t)
	queueAJob := submitJob(t, service, "alice", "queue-a", "a.txt", "queue-a-document", nil)
	queueBJob := submitJob(t, service, "alice", "queue-b", "b.txt", "queue-b-document", nil)

	result, err := service.PrintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  queueAJob.ID,
		AttrJobURI: queueBJob.DocumentURI,
	})
	if err == nil {
		t.Fatalf("cross-queue reference printed %q", result.Document)
	}
	if !errors.Is(err, ErrURIOutsideJobStorage) && !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-queue error = %v", err)
	}
	result, err = service.Print(context.Background(), "alice", queueBJob.ID, nil)
	if err != nil || string(result.Document) != "queue-b-document" {
		t.Fatalf("own queue-b print = %q, %v", result.Document, err)
	}
}

func TestURIReplacementAndCrossQueueReferencesRejected(t *testing.T) {
	service, _ := newMemoryTestService(t)
	aliceJob := submitJob(t, service, "alice", "queue-a", "alice.txt", "alice-only", nil)
	bobJob := submitJob(t, service, "bob", "queue-b", "bob.txt", "bob-only", nil)

	attacks := []map[string]string{
		{AttrJobURI: bobJob.DocumentURI},
		{AttrDocumentURI: bobJob.DocumentURI},
		{AttrQueue: bobJob.Queue},
		{AttrSubmitter: bobJob.Submitter},
		{AttrJobID: bobJob.ID},
		{AttrSHA256: bobJob.Digest},
		{AttrContentDigest: bobJob.Digest},
		{AttrDocumentName: bobJob.DocumentName},
		{AttrJobURI: bobJob.DocumentURI, AttrDocumentURI: aliceJob.DocumentURI},
	}
	for i, attributes := range attacks {
		result, err := service.Print(context.Background(), "alice", aliceJob.ID, attributes)
		if err == nil {
			t.Fatalf("attack %d was accepted and returned %q", i, result.Document)
		}
		if len(result.Document) != 0 {
			t.Fatalf("attack %d returned document bytes", i)
		}
	}

	// Failed attempts must not corrupt the original job.
	result, err := service.Print(context.Background(), "alice", aliceJob.ID, nil)
	if err != nil {
		t.Fatalf("legitimate print after attacks: %v", err)
	}
	if string(result.Document) != "alice-only" {
		t.Fatalf("printed %q", result.Document)
	}
}

func TestCrossUserCancelAndReprintRejected(t *testing.T) {
	service, _ := newMemoryTestService(t)
	aliceJob := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)

	if _, err := service.Cancel(context.Background(), "bob", aliceJob.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob cancel error = %v, want ErrForbidden", err)
	}
	if _, err := service.Reprint(context.Background(), "bob", aliceJob.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob reprint error = %v, want ErrForbidden", err)
	}
	if _, err := service.Print(context.Background(), "bob", aliceJob.ID, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob print error = %v, want ErrForbidden", err)
	}
	if _, err := service.Get(context.Background(), "bob", aliceJob.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob get error = %v, want ErrForbidden", err)
	}

	result, err := service.Print(context.Background(), "alice", aliceJob.ID, nil)
	if err != nil {
		t.Fatalf("alice print after unauthorized attempts: %v", err)
	}
	if string(result.Document) != "alice-only" {
		t.Fatalf("printed %q", result.Document)
	}
}

func TestCancelBlocksPrintAndReprint(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)

	cancelled, err := service.Cancel(context.Background(), "alice", job.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", cancelled.Status)
	}
	if _, err := service.Print(context.Background(), "alice", job.ID, nil); !errors.Is(err, ErrJobCancelled) {
		t.Fatalf("print after cancel error = %v, want ErrJobCancelled", err)
	}
	if _, err := service.Reprint(context.Background(), "alice", job.ID); !errors.Is(err, ErrJobCancelled) {
		t.Fatalf("reprint after cancel error = %v, want ErrJobCancelled", err)
	}
	again, err := service.Cancel(context.Background(), "alice", job.ID)
	if err != nil || again.Status != StatusCancelled {
		t.Fatalf("idempotent cancel = %+v, %v", again, err)
	}
}

func TestReprintKeepsOriginalBinding(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)
	bobJob := submitJob(t, service, "bob", "q2", "bob.txt", "bob-only", nil)

	if _, err := service.Print(context.Background(), "alice", job.ID, nil); err != nil {
		t.Fatalf("first print: %v", err)
	}
	if _, err := service.Print(context.Background(), "alice", job.ID, nil); !errors.Is(err, ErrJobCompleted) {
		t.Fatalf("second direct print error = %v, want ErrJobCompleted", err)
	}
	if _, err := service.ReprintWithAttributes(context.Background(), "alice", job.ID, map[string]string{
		AttrJobURI: bobJob.DocumentURI,
	}); err == nil {
		t.Fatal("reprint accepted a replacement URI")
	}
	current, err := service.Get(context.Background(), "alice", job.ID)
	if err != nil || current.Status != StatusCompleted {
		t.Fatalf("failed reprint changed state: %+v, %v", current, err)
	}

	reprinted, err := service.ReprintWithAttributes(context.Background(), "alice", job.ID, map[string]string{
		AttrJobURI:    job.DocumentURI,
		AttrQueue:     job.Queue,
		AttrSubmitter: job.Submitter,
		AttrSHA256:    job.Digest,
	})
	if err != nil {
		t.Fatalf("Reprint: %v", err)
	}
	if reprinted.Status != StatusPending {
		t.Fatalf("reprinted status = %s, want pending", reprinted.Status)
	}
	if reprinted.DocumentURI != job.DocumentURI || reprinted.Digest != job.Digest ||
		reprinted.Queue != job.Queue || reprinted.Submitter != job.Submitter {
		t.Fatalf("reprint changed binding: %+v", reprinted)
	}

	result, err := service.Print(context.Background(), "alice", job.ID, nil)
	if err != nil {
		t.Fatalf("print after reprint: %v", err)
	}
	if string(result.Document) != "alice-only" {
		t.Fatalf("printed %q", result.Document)
	}
}

func TestCancelWithAttributesRejectsReplacement(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)
	bobJob := submitJob(t, service, "bob", "q2", "bob.txt", "bob-only", nil)

	if _, err := service.CancelWithAttributes(context.Background(), "alice", job.ID, map[string]string{
		AttrJobURI: bobJob.DocumentURI,
	}); err == nil {
		t.Fatal("cancel accepted a replacement URI")
	}
	result, err := service.Print(context.Background(), "alice", job.ID, nil)
	if err != nil {
		t.Fatalf("print after rejected cancel: %v", err)
	}
	if string(result.Document) != "alice-only" {
		t.Fatalf("printed %q", result.Document)
	}
}

func TestSubmitBindingInjectionRejected(t *testing.T) {
	service, _ := newMemoryTestService(t)
	bobJob := submitJob(t, service, "bob", "queue-b", "bob.txt", "bob-only", nil)

	attacks := []map[string]string{
		{AttrQueue: bobJob.Queue},
		{AttrSubmitter: bobJob.Submitter},
		{AttrJobURI: bobJob.DocumentURI},
		{AttrDocumentURI: bobJob.DocumentURI},
		{AttrSHA256: bobJob.Digest},
		{AttrDocumentName: bobJob.DocumentName},
		{"role": "admin"},
	}
	for i, attributes := range attacks {
		_, err := service.Submit(context.Background(), "alice", SubmitRequest{
			Queue:        "queue-a",
			DocumentName: "alice.txt",
			Content:      []byte("alice-only"),
			Attributes:   attributes,
		})
		if err == nil {
			t.Fatalf("submit attack %d was accepted", i)
		}
	}
}

func TestSubmitAcceptsConsistentBindingAttributes(t *testing.T) {
	service, _ := newMemoryTestService(t)
	content := []byte("alice-only")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	const id = "job-fixed-binding"
	uri, err := CanonicalJobURI(Scope{Queue: "queue-a", Submitter: "alice", JobID: id}, "alice.txt")
	if err != nil {
		t.Fatalf("CanonicalJobURI: %v", err)
	}

	job, err := service.Submit(context.Background(), "alice", SubmitRequest{
		ID:           id,
		Queue:        "queue-a",
		DocumentName: "alice.txt",
		Content:      content,
		Attributes: map[string]string{
			AttrJobID:        id,
			AttrQueue:        "queue-a",
			AttrSubmitter:    "alice",
			AttrJobURI:       uri,
			AttrSHA256:       digest,
			AttrDocumentName: "alice.txt",
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if job.DocumentURI != uri || job.Digest != digest {
		t.Fatalf("unexpected binding: %+v", job)
	}
	result, err := service.Print(context.Background(), "alice", job.ID, map[string]string{AttrJobURI: uri})
	if err != nil || !bytes.Equal(result.Document, content) {
		t.Fatalf("Print = %q, %v", result.Document, err)
	}
}

func TestAttributeInjectionRejected(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	bad := []map[string]string{
		{"Job-URI": "job:///q/u/j/d"},
		{"job_uri": "job:///q/u/j/d"},
		{"__proto__": "x"},
		{"queue ": "q1"},
		{"": "q1"},
		{AttrCopies: "0"},
		{AttrCopies: "01"},
		{AttrCopies: "1000"},
		{AttrCopies: "1e3"},
		{AttrQueue: "../queue"},
		{AttrQueue: "queue/other"},
		{AttrQueue: "queue\x00other"},
		{AttrSubmitter: "alice\nbob"},
		{AttrSHA256: strings.ToUpper(validDigest)},
		{AttrSHA256: "abc"},
		{AttrTitle: "hello\x00world"},
		{AttrTitle: "line\nbreak"},
		{AttrSides: "both"},
		{AttrJobURI: "job://queue/user/job/doc"},
		{AttrJobURI: "job:///q/u/j/doc?download=1"},
		{AttrJobURI: "job:///q/u/j/doc#fragment"},
		{AttrJobURI: "job:///q/u/j/../doc"},
		{AttrJobURI: "job:///q/u/j/doc%2Fother"},
		{AttrJobURI: "JOB:///q/u/j/doc"},
		{AttrJobURI: "job:///q/u/j/d", AttrDocumentURI: "job:///q/u/j/e"},
		{AttrSHA256: validDigest, AttrContentDigest: strings.Repeat("b", 64)},
	}
	for i, attributes := range bad {
		parsed, err := ParseAttributes(attributes)
		if err == nil {
			t.Fatalf("injection %d was accepted: %+v", i, parsed.Values())
		}
	}
}

func TestDigestMismatchBlocksPrint(t *testing.T) {
	service, store := newFileTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)
	dir, err := store.JobDirectory(job.Scope())
	if err != nil {
		t.Fatalf("JobDirectory: %v", err)
	}
	path := filepath.Join(dir, job.DocumentName)
	if err := os.WriteFile(path, []byte("tampered-or-other-user-document"), 0o600); err != nil {
		t.Fatalf("tamper document: %v", err)
	}
	result, err := service.Print(context.Background(), "alice", job.ID, nil)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("print tampered document error = %v, want ErrDigestMismatch", err)
	}
	if len(result.Document) != 0 {
		t.Fatal("tampered document bytes were returned")
	}
	if err := os.WriteFile(path, []byte("alice-only"), 0o600); err != nil {
		t.Fatalf("restore document: %v", err)
	}
	result, err = service.Print(context.Background(), "alice", job.ID, nil)
	if err != nil || string(result.Document) != "alice-only" {
		t.Fatalf("print restored document = %q, %v", result.Document, err)
	}
}

type recordingStorage struct {
	inner Storage
	mu    sync.Mutex
	scope Scope
	name  string
	reads int
}

func (r *recordingStorage) WriteJob(ctx context.Context, scope Scope, documentName string, content []byte) error {
	return r.inner.WriteJob(ctx, scope, documentName, content)
}

func (r *recordingStorage) ReadJob(ctx context.Context, scope Scope, documentName string) ([]byte, error) {
	r.mu.Lock()
	r.scope = scope
	r.name = documentName
	r.reads++
	r.mu.Unlock()
	return r.inner.ReadJob(ctx, scope, documentName)
}

func (r *recordingStorage) DeleteJob(ctx context.Context, scope Scope) error {
	return r.inner.DeleteJob(ctx, scope)
}

func (r *recordingStorage) last() (Scope, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scope, r.name, r.reads
}

func TestPrintUsesStoredBindingNotRequestAttributes(t *testing.T) {
	inner := NewMemoryStorage()
	store := &recordingStorage{inner: inner}
	service, err := NewService(store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)

	if _, err := service.Print(context.Background(), "alice", job.ID, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	scope, name, reads := store.last()
	if reads != 1 || scope != job.Scope() || name != job.DocumentName {
		t.Fatalf("storage read used scope=%+v name=%q reads=%d", scope, name, reads)
	}
}

func TestReturnedJobCannotMutateServiceBinding(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", map[string]string{
		AttrCopies: "1",
	})
	job.Attributes.values[AttrCopies] = "999"
	job.DocumentURI = "job:///evil/evil/evil/evil.txt"
	job.Digest = strings.Repeat("0", 64)

	result, err := service.Print(context.Background(), "alice", job.ID, nil)
	if err != nil {
		t.Fatalf("Print after mutating returned copy: %v", err)
	}
	if string(result.Document) != "alice-only" {
		t.Fatalf("printed %q", result.Document)
	}
	if got, _ := result.Attributes.Get(AttrCopies); got != "1" {
		t.Fatalf("service copy was mutated: copies=%q", got)
	}
}

func TestPrintByAttributesUsesOriginalBinding(t *testing.T) {
	service, _ := newMemoryTestService(t)
	aliceJob := submitJob(t, service, "alice", "queue-a", "alice.txt", "alice-only", nil)
	bobJob := submitJob(t, service, "bob", "queue-b", "bob.txt", "bob-only", nil)

	// URI replacement inside the attribute set is rejected.
	if _, err := service.PrintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  aliceJob.ID,
		AttrJobURI: bobJob.DocumentURI,
	}); !errors.Is(err, ErrURIOutsideJobStorage) {
		t.Fatalf("replacement URI error = %v, want ErrURIOutsideJobStorage", err)
	}

	// A URI-only lookup cannot be used by a different user.
	if _, err := service.PrintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobURI: bobJob.DocumentURI,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-user URI lookup error = %v, want ErrForbidden", err)
	}

	// Missing selector attributes fail closed.
	if _, err := service.PrintByAttributes(context.Background(), "alice", map[string]string{
		AttrCopies: "1",
	}); !errors.Is(err, ErrInvalidAttribute) {
		t.Fatalf("missing selector error = %v, want ErrInvalidAttribute", err)
	}

	// A job-id plus a consistent job-uri is the normal queue attribute shape.
	result, err := service.PrintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  aliceJob.ID,
		AttrJobURI: aliceJob.DocumentURI,
		AttrCopies: "3",
	})
	if err != nil || string(result.Document) != "alice-only" {
		t.Fatalf("PrintByAttributes = %q, %v", result.Document, err)
	}
}

func TestCancelAndReprintByAttributes(t *testing.T) {
	service, _ := newMemoryTestService(t)
	job := submitJob(t, service, "alice", "q1", "alice.txt", "alice-only", nil)
	bobJob := submitJob(t, service, "bob", "q2", "bob.txt", "bob-only", nil)

	if _, err := service.CancelByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  job.ID,
		AttrJobURI: bobJob.DocumentURI,
	}); err == nil {
		t.Fatal("CancelByAttributes accepted a replacement URI")
	}
	if _, err := service.CancelByAttributes(context.Background(), "bob", map[string]string{
		AttrJobID: job.ID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-user cancel error = %v, want ErrForbidden", err)
	}

	if _, err := service.Print(context.Background(), "alice", job.ID, nil); err != nil {
		t.Fatalf("initial print: %v", err)
	}
	if _, err := service.ReprintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  job.ID,
		AttrJobURI: bobJob.DocumentURI,
	}); err == nil {
		t.Fatal("ReprintByAttributes accepted a replacement URI")
	}
	reprinted, err := service.ReprintByAttributes(context.Background(), "alice", map[string]string{
		AttrJobID:  job.ID,
		AttrJobURI: job.DocumentURI,
	})
	if err != nil {
		t.Fatalf("ReprintByAttributes: %v", err)
	}
	if reprinted.DocumentURI != job.DocumentURI || reprinted.Status != StatusPending {
		t.Fatalf("reprint binding changed: %+v", reprinted)
	}
}
