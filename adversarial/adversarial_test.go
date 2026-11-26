package adversarial_test

import (
	"testing"

	"formal.local/w27-6467acc7b4/adversarial"
	"formal.local/w27-6467acc7b4/resolve"
)

func TestSearcherFindsPrefixEscapesAndSecureHolds(t *testing.T) {
	sc := adversarial.BuildStandardScenario()
	searcher := adversarial.NewSearcher(sc)
	report := searcher.Run()

	if report.Sequences == 0 || report.Interleavings == 0 {
		t.Fatalf("searcher did not enumerate anything: %+v", report)
	}
	if len(report.Findings) == 0 {
		t.Fatal("expected at least one demonstrated escape against the prefix resolver")
	}
	if report.SecureEscapes != 0 {
		t.Fatalf("secure resolver escaped %d times", report.SecureEscapes)
	}

	// Every finding must come with a bounded minimal sequence and a concrete
	// host node outside the container.
	minLen := 7
	for _, finding := range report.Findings {
		if len(finding.Sequence) > adversarial.MaxSteps {
			t.Fatalf("sequence exceeds bound: %d", len(finding.Sequence))
		}
		if len(finding.Sequence) < minLen {
			minLen = len(finding.Sequence)
		}
		if finding.HostNode == nil {
			t.Fatal("finding missing host node")
		}
		if finding.HostNode != sc.HostSecret {
			t.Fatalf("unexpected escape target: %s", finding.HostPath)
		}
		// The secure resolver must have either refused or stayed in-root.
		if finding.SecureError == nil && finding.SecurePath == "" {
			t.Fatal("finding lacks secure-resolver verdict")
		}
	}
	t.Logf("minimum escape sequence length: %d; states=%d sequences=%d interleavings=%d findings=%d",
		minLen, report.StatesVisited, report.Sequences, report.Interleavings, len(report.Findings))
}

func TestExhaustiveMicroTreeSecureZeroEscape(t *testing.T) {
	sc := adversarial.ExhaustiveMicroTree()
	searcher := adversarial.NewSearcher(sc)
	report := searcher.Run()

	if report.SecureEscapes != 0 {
		t.Fatalf("secure resolver escaped on %d exhaustive sequences", report.SecureEscapes)
	}
	if report.StatesVisited < 5 {
		t.Fatalf("exhaustive search covered too few states: %d", report.StatesVisited)
	}
	t.Logf("micro tree: states=%d sequences=%d interleavings=%d prefix-findings=%d",
		report.StatesVisited, report.Sequences, report.Interleavings, len(report.Findings))
}

func TestSearcherIsDeterministic(t *testing.T) {
	first := adversarial.NewSearcher(adversarial.ExhaustiveMicroTree()).Run()
	second := adversarial.NewSearcher(adversarial.ExhaustiveMicroTree()).Run()
	if first.StatesVisited != second.StatesVisited ||
		first.Sequences != second.Sequences ||
		first.Interleavings != second.Interleavings ||
		len(first.Findings) != len(second.Findings) {
		t.Fatal("searcher output is not deterministic across runs")
	}
}

func TestAllFindingsReplaySecurely(t *testing.T) {
	sc := adversarial.BuildStandardScenario()
	_ = adversarial.NewSearcher(sc).Run()

	// Replay every open path directly against the secure resolver to prove
	// none of them can reach the host secret regardless of sequence.
	for _, path := range sc.OpenPaths {
		res, err := sc.Secure.Open(path, resolve.DefaultSecureFlags(), sc.Cred)
		if err == nil && res.Node == sc.HostSecret {
			t.Fatalf("secure resolver exposed host secret via %s", path)
		}
	}
}
