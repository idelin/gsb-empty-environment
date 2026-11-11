package crashfp

import (
	"errors"
	"testing"
)

func TestPermutationIndependentClassification(t *testing.T) {
	reports := buildReports()
	permutations := 0

	var permute func(int)
	permute = func(start int) {
		if start == len(reports) {
			permutations++
			classifier := NewClassifier(nil)
			for _, report := range reports {
				if err := classifier.Add(report); err != nil {
					t.Fatalf("add %s: %v", report.ID, err)
				}
			}
			assertExpectedPartition(t, classifier)
			assertExpectedCandidates(t, classifier)
			assertEquivalenceRelation(t, classifier)
			return
		}

		for index := start; index < len(reports); index++ {
			reports[start], reports[index] = reports[index], reports[start]
			permute(start + 1)
			reports[start], reports[index] = reports[index], reports[start]
		}
	}

	permute(0)
	if permutations != 5040 {
		t.Fatalf("expected 5040 permutations, got %d", permutations)
	}
}

func assertExpectedPartition(t *testing.T, classifier *Classifier) {
	t.Helper()

	classes := classifier.Classes()
	want := expectedClasses()
	if len(classes) != len(want) {
		t.Fatalf("classes = %d, want %d: %+v", len(classes), len(want), classes)
	}

	seen := make(map[string]bool)
	for _, class := range classes {
		expected, ok := want[class.ID]
		if !ok {
			t.Fatalf("unexpected class id %q", class.ID)
		}
		seen[class.ID] = true
		if !equalStrings(class.ReportIDs, expected) {
			t.Fatalf("class %s = %v, want %v", class.ID, class.ReportIDs, expected)
		}
		for _, id := range class.ReportIDs {
			if classifier.ClassOf(id) != class.ID {
				t.Fatalf("ClassOf(%s) = %q, want %q", id, classifier.ClassOf(id), class.ID)
			}
		}
		if class.Fingerprint == "" {
			t.Fatalf("class %s has empty fingerprint", class.ID)
		}
	}

	for classID := range want {
		if !seen[classID] {
			t.Fatalf("missing class %q", classID)
		}
	}

	if classifier.ReportCount() != 7 {
		t.Fatalf("report count = %d, want 7", classifier.ReportCount())
	}
}

func assertExpectedCandidates(t *testing.T, classifier *Classifier) {
	t.Helper()

	candidates := classifier.Candidates()
	want := expectedCandidates()
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %d, want %d: %+v", len(candidates), len(want), candidates)
	}

	for _, candidate := range candidates {
		key := [2]string{candidate.ClassA, candidate.ClassB}
		reason, ok := want[key]
		if !ok {
			t.Fatalf("unexpected candidate %v", candidate)
		}
		if candidate.Reason != reason {
			t.Fatalf("candidate %v reason = %q, want %q", key, candidate.Reason, reason)
		}
		if candidate.SharedFrames < 1 {
			t.Fatalf("candidate %v has no shared frames", candidate)
		}
		if candidate.ClassA == candidate.ClassB {
			t.Fatalf("candidate pair %v was merged into one class", key)
		}
	}
}

func assertEquivalenceRelation(t *testing.T, classifier *Classifier) {
	t.Helper()

	ids := make([]string, 0, len(classifier.entries))
	for _, item := range classifier.entries {
		ids = append(ids, item.id)
	}

	equivalent := func(a, b string) bool {
		return classifier.ClassOf(a) == classifier.ClassOf(b)
	}

	for _, id := range ids {
		if !equivalent(id, id) {
			t.Fatalf("reflexivity failed for %s", id)
		}
	}

	for _, a := range ids {
		for _, b := range ids {
			if equivalent(a, b) != equivalent(b, a) {
				t.Fatalf("symmetry failed for %s/%s", a, b)
			}
			for _, c := range ids {
				if equivalent(a, b) && equivalent(b, c) && !equivalent(a, c) {
					t.Fatalf("transitivity failed for %s/%s/%s", a, b, c)
				}
			}
		}
	}

	assigned := 0
	for _, class := range classifier.Classes() {
		assigned += len(class.ReportIDs)
	}
	if assigned != len(ids) {
		t.Fatalf("partition covers %d of %d reports", assigned, len(ids))
	}
}

func TestNormalizeRemovesASLR(t *testing.T) {
	first, err := Normalize(appLibStack(0x1200, 0x4800), modules(0x1000, 0x4000))
	if err != nil {
		t.Fatal(err)
	}

	second, err := Normalize(appLibStack(0x9200, 0xc800), modules(0x9000, 0xc000))
	if err != nil {
		t.Fatal(err)
	}

	if !framesEqual(first.Frames, second.Frames) {
		t.Fatalf("normalized frames differ:\n%+v\n%+v", first.Frames, second.Frames)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("same logical stack produced different fingerprints")
	}

	wantOffsets := []uint64{0x200, 0x800, 0x840}
	for index, normalized := range first.Frames {
		if normalized.Offset != wantOffsets[index] {
			t.Fatalf("frame %d offset = %#x, want %#x", index, normalized.Offset, wantOffsets[index])
		}
	}
	if len(first.Frames[0].Inlines) != 2 {
		t.Fatalf("inline chain lost: %+v", first.Frames[0].Inlines)
	}

	second.Frames[0].Inlines[0].Line = 9999
	if first.Frames[0].Inlines[0].Line == second.Frames[0].Inlines[0].Line {
		t.Fatal("normalized stack aliases inline chain input")
	}
}

func TestNormalizeErrors(t *testing.T) {
	frames := appLibStack(0x1200, 0x4800)
	complete := modules(0x1000, 0x4000)

	if _, err := Normalize(nil, complete); !errors.Is(err, ErrEmptyStack) {
		t.Fatalf("empty stack error = %v, want %v", err, ErrEmptyStack)
	}
	if _, err := Normalize(frames, complete[1:]); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("unknown module error = %v, want %v", err, ErrUnknownModule)
	}

	lowBase := []ModuleLoad{
		{Name: "app", Digest: digestApp, Base: 0x9000},
		{Name: "lib", Digest: digestLib, Base: 0x4000},
	}
	if _, err := Normalize(frames, lowBase); !errors.Is(err, ErrAddressBeforeBase) {
		t.Fatalf("low base error = %v, want %v", err, ErrAddressBeforeBase)
	}
}

func TestClassifierValidationAndTruncation(t *testing.T) {
	classifier := NewClassifier(nil)
	valid := Report{
		ID:      "x",
		Modules: modules(0x1000, 0x4000),
		Frames:  appLibStack(0x1200, 0x4800),
	}

	if err := classifier.Add(Report{ID: "", Modules: valid.Modules, Frames: valid.Frames}); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("missing id error = %v, want %v", err, ErrInvalidReport)
	}
	if err := classifier.Add(valid); err != nil {
		t.Fatal(err)
	}
	if err := classifier.Add(valid); !errors.Is(err, ErrDuplicateReport) {
		t.Fatalf("duplicate error = %v, want %v", err, ErrDuplicateReport)
	}
	if err := classifier.AddStack("nil", "X", nil); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("nil stack error = %v, want %v", err, ErrInvalidReport)
	}
	if got := classifier.ClassOf("missing"); got != "" {
		t.Fatalf("unknown ClassOf = %q, want empty", got)
	}
	if exception, ok := classifier.Exception("x"); !ok || exception != "" {
		t.Fatalf("exception lookup = %q/%v, want empty/true", exception, ok)
	}

	// A complete stack whose leaf equals another stack's leaf is still a
	// distinct defect: no truncated-suffix candidate and no merge.
	completeSuffix := &Stack{
		Frames: []Frame{
			{ModuleDigest: digestOther, Offset: 0x100, Function: "other.Entry", File: "other.c", Line: 9},
			{ModuleDigest: digestLib, Offset: 0x840, Function: "lib.Eval", File: "eval.c", Line: 57},
		},
	}
	if err := classifier.AddStack("short-complete", "SIGSEGV", completeSuffix); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range classifier.Candidates() {
		if candidate.Reason == ReasonTruncatedSuffix {
			t.Fatalf("complete stack generated truncated candidate: %+v", candidate)
		}
	}

	// A truncated observation with an exact known suffix is retained only as
	// an uncertain candidate, never promoted to an equivalence merge.
	truncatedStack := &Stack{
		Frames: []Frame{
			{ModuleDigest: digestLib, Offset: 0x800, Function: "lib.Parse", File: "parse.c", Line: 130},
			{ModuleDigest: digestLib, Offset: 0x840, Function: "lib.Eval", File: "eval.c", Line: 57},
		},
		Truncated: true,
	}
	if err := classifier.AddStack("short-truncated", "SIGSEGV", truncatedStack); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, candidate := range classifier.Candidates() {
		pair := [2]string{candidate.ClassA, candidate.ClassB}
		if pair == [2]string{"short-truncated", "x"} &&
			candidate.Reason == ReasonTruncatedSuffix && candidate.SharedFrames == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("truncated suffix candidate missing: %+v", classifier.Candidates())
	}
	if classifier.ClassOf("x") == classifier.ClassOf("short-truncated") {
		t.Fatal("truncated compatibility was promoted to an equivalence merge")
	}
}

func TestFingerprintIgnoresTruncationButNotContent(t *testing.T) {
	stack, err := Normalize(appLibStack(0x1200, 0x4800), modules(0x1000, 0x4000))
	if err != nil {
		t.Fatal(err)
	}
	copyStack := cloneStack(stack)
	copyStack.Truncated = true

	if stack.Fingerprint() != copyStack.Fingerprint() {
		t.Fatal("truncation flag changed the fingerprint")
	}

	copyStack.Frames[1].Offset++
	if stack.Fingerprint() == copyStack.Fingerprint() {
		t.Fatal("different relative offsets produced the same fingerprint")
	}
}

func TestMinSuffixFramesOption(t *testing.T) {
	reports := buildReports()
	classifier := NewClassifier(&Options{MinSuffixFrames: 2})
	for _, report := range reports {
		if err := classifier.Add(report); err != nil {
			t.Fatal(err)
		}
	}

	for _, candidate := range classifier.Candidates() {
		if candidate.SharedFrames < 2 {
			t.Fatalf("threshold violated by candidate %v", candidate)
		}
	}

	// r1/r6 share only the leaf frame, so the threshold filters them out.
	for _, candidate := range classifier.Candidates() {
		if (candidate.ClassA == "r1" && candidate.ClassB == "r6") ||
			(candidate.ClassA == "r5" && candidate.ClassB == "r6") {
			t.Fatalf("single-frame suffix retained under threshold: %v", candidate)
		}
	}

	// Hard equivalence grouping is unaffected by the candidate threshold.
	assertExpectedPartition(t, classifier)
}
