package artifactflow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func mustVerify(t *testing.T, set *ReceiptSet) *Result {
	t.Helper()
	result, err := Verify(set)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return result
}

func requireStatus(t *testing.T, result *Result, want Status) {
	t.Helper()
	if result.Status != want {
		t.Fatalf("status = %s, want %s (problems: %+v, ambiguity: %+v)", result.Status, want, result.Problems, result.Ambiguity)
	}
}

func TestSameDigestDualSourceIsAmbiguous(t *testing.T) {
	set := &ReceiptSet{
		Roots: []RootArtifact{
			{ID: "source-a", Path: "src/app.tar", Digest: "sha256:payload", Signer: "builder"},
			{ID: "source-b", Path: "src/app.tar", Digest: "sha256:payload", Signer: "builder"},
		},
		Receipts: []Receipt{{
			ID:     "extract",
			Step:   "extract",
			Signer: "builder",
			Inputs: []InputPort{{Path: "src/app.tar", Digest: "sha256:payload", Consumption: Consume}},
			Outputs: []OutputPort{{
				Path:   "bin/app",
				Digest: "sha256:binary",
			}},
		}},
		Finals: []FinalArtifact{{Path: "bin/app", Digest: "sha256:binary", Sensitive: true}},
	}

	result := mustVerify(t, set)
	requireStatus(t, result, StatusAmbiguous)
	amb := result.Ambiguity
	if amb == nil {
		t.Fatal("missing ambiguity detail")
	}
	if equalStrings(amb.First.Roots, amb.Second.Roots) {
		t.Fatalf("ambiguous attributions have identical roots: %v", amb.First.Roots)
	}
	want := map[string]bool{"source-a": true, "source-b": true}
	for _, roots := range [][]string{amb.First.Roots, amb.Second.Roots} {
		if len(roots) != 1 || !want[roots[0]] {
			t.Fatalf("unexpected attribution roots: %v", roots)
		}
	}
	if err := VerifyCertificate(set, amb.First.Certificate); err != nil {
		t.Fatalf("first ambiguity witness is not independently valid: %v", err)
	}
	if err := VerifyCertificate(set, amb.Second.Certificate); err != nil {
		t.Fatalf("second ambiguity witness is not independently valid: %v", err)
	}
}

func TestOneShotInputCannotFeedTwoSteps(t *testing.T) {
	base := func(consumption Consumption) *ReceiptSet {
		return &ReceiptSet{
			Roots: []RootArtifact{{
				ID: "shared-root", Path: "src/root", Digest: "sha256:root", Signer: "builder",
			}},
			Receipts: []Receipt{
				{
					ID: "left", Step: "left", Signer: "builder",
					Inputs:  []InputPort{{Path: "src/root", Digest: "sha256:root", Consumption: consumption}},
					Outputs: []OutputPort{{Path: "out/left", Digest: "sha256:left"}},
				},
				{
					ID: "right", Step: "right", Signer: "builder",
					Inputs:  []InputPort{{Path: "src/root", Digest: "sha256:root", Consumption: consumption}},
					Outputs: []OutputPort{{Path: "out/right", Digest: "sha256:right"}},
				},
			},
			Finals: []FinalArtifact{
				{Path: "out/left", Digest: "sha256:left", Sensitive: true},
				{Path: "out/right", Digest: "sha256:right", Sensitive: true},
			},
		}
	}

	t.Run("consume rejects", func(t *testing.T) {
		result := mustVerify(t, base(Consume))
		requireStatus(t, result, StatusRejected)
		if len(result.Problems) == 0 || result.Problems[0].Code != ProblemNoConsistentBuild {
			t.Fatalf("unexpected problems: %+v", result.Problems)
		}
	})

	t.Run("share forks", func(t *testing.T) {
		result := mustVerify(t, base(Share))
		requireStatus(t, result, StatusVerified)
		if err := VerifyCertificate(base(Share), result.Certificate); err != nil {
			t.Fatalf("certificate: %v", err)
		}
		for _, roots := range result.RootSets {
			if len(roots.Roots) != 1 || roots.Roots[0] != "shared-root" {
				t.Fatalf("forked provenance was not retained: %+v", roots)
			}
		}
	})
}

func TestCapturedRenameAndNormalization(t *testing.T) {
	build := func(input string) *ReceiptSet {
		return &ReceiptSet{
			Roots: []RootArtifact{{
				ID: "stage-root", Tenant: "tenant-a", Path: "./stage/alpha/app", Digest: "sha256:staged", Signer: "builder",
			}},
			Receipts: []Receipt{{
				ID: "promote", Step: "promote", Tenant: "tenant-a", Signer: "releaser",
				Inputs:  []InputPort{{Path: input, Digest: "sha256:staged", Consumption: Consume}},
				Outputs: []OutputPort{{Path: "./release/alpha/app", Digest: "sha256:released"}},
				Renames: []RenameRule{{
					From: "./stage/{name}/app",
					To:   "./release/{name}/app",
				}},
			}},
			Finals: []FinalArtifact{{
				Tenant: "tenant-a", Path: "./release/alpha/app", Digest: "sha256:released", Sensitive: true,
			}},
		}
	}

	t.Run("same captured name", func(t *testing.T) {
		set := build("./release/alpha/app")
		result := mustVerify(t, set)
		requireStatus(t, result, StatusVerified)
		if err := result.Certificate.Verify(set); err != nil {
			t.Fatalf("certificate: %v", err)
		}
		if len(result.RootSets) != 1 || !equalStrings(result.RootSets[0].Roots, []string{"stage-root"}) {
			t.Fatalf("unexpected root sets: %+v", result.RootSets)
		}
	})

	t.Run("crossed captured name is rejected", func(t *testing.T) {
		result := mustVerify(t, build("./release/beta/app"))
		requireStatus(t, result, StatusRejected)
	})

	t.Run("tenant roots cannot be crossed", func(t *testing.T) {
		set := build("./release/alpha/app")
		set.Roots[0].Tenant = "tenant-b"
		result := mustVerify(t, set)
		requireStatus(t, result, StatusRejected)
	})

	t.Run("dot dot is rejected", func(t *testing.T) {
		set := build("./release/alpha/app")
		set.Roots[0].Path = "../tenant-b/stage/alpha/app"
		if _, err := Verify(set); err == nil {
			t.Fatal("expected tenant-root escape to be rejected")
		}
	})

	t.Run("normalization removes only dot components", func(t *testing.T) {
		if got, err := normalizeLogicalPath("./a/./b"); err != nil || got != "a/b" {
			t.Fatalf("normalize = %q, %v", got, err)
		}
		for _, bad := range []string{"", "/a", "a//b", "a/../b", "a/.."} {
			if _, err := normalizeLogicalPath(bad); err == nil {
				t.Fatalf("expected %q to be rejected", bad)
			}
		}
	})
}

func TestCapturedPathsDoNotCrossBetweenOutputs(t *testing.T) {
	set := &ReceiptSet{
		Receipts: []Receipt{
			{
				ID: "stage-a", Step: "stage-a", Signer: "builder",
				Outputs: []OutputPort{{Path: "stage/a", Digest: "sha256:same"}},
			},
			{
				ID: "stage-b", Step: "stage-b", Signer: "builder",
				Outputs: []OutputPort{{Path: "stage/b", Digest: "sha256:same"}},
			},
			{
				ID: "promote", Step: "promote", Signer: "releaser",
				Inputs: []InputPort{
					{Path: "release/a", Digest: "sha256:same", Consumption: Share},
					{Path: "release/b", Digest: "sha256:same", Consumption: Share},
				},
				Outputs: []OutputPort{
					{Path: "final/a", Digest: "sha256:final"},
					{Path: "final/b", Digest: "sha256:final"},
				},
				Renames: []RenameRule{
					{From: "stage/{name}", To: "release/{name}"},
					{From: "release/{name}", To: "final/{name}"},
				},
			},
		},
		Finals: []FinalArtifact{
			{Path: "final/a", Digest: "sha256:final", Sensitive: true},
			{Path: "final/b", Digest: "sha256:final", Sensitive: true},
		},
	}

	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	records := finalRecordsByIndex(result.Certificate)
	if !equalStrings(records[0].Roots, []string{"receipt:stage-a"}) {
		t.Fatalf("final/a was attributed to the wrong stage: %v", records[0].Roots)
	}
	if !equalStrings(records[1].Roots, []string{"receipt:stage-b"}) {
		t.Fatalf("final/b was attributed to the wrong stage: %v", records[1].Roots)
	}
}

func TestUnboundInputsKeepTheirProvenance(t *testing.T) {
	set := &ReceiptSet{
		Roots: []RootArtifact{
			{ID: "primary", Path: "src/a", Digest: "p", Signer: "builder"},
			{ID: "aux", Path: "aux/b", Digest: "x", Signer: "helper"},
		},
		Receipts: []Receipt{{
			ID: "combine", Step: "combine", Signer: "packager",
			Inputs: []InputPort{
				{Path: "src/a", Digest: "p", Consumption: Share},
				{Path: "aux/b", Digest: "x", Consumption: Share},
			},
			Outputs: []OutputPort{{Path: "dst/a", Digest: "out"}},
			Renames: []RenameRule{{From: "src/{name}", To: "dst/{name}"}},
		}},
		Finals: []FinalArtifact{{Path: "dst/a", Digest: "out", Sensitive: true}},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	if !equalStrings(result.Certificate.Finals[0].Roots, []string{"aux", "primary"}) {
		t.Fatalf("unbound input provenance was dropped: %v", result.Certificate.Finals[0].Roots)
	}
}

func TestExactRenameRule(t *testing.T) {
	build := func(rule RenameRule) *ReceiptSet {
		return &ReceiptSet{
			Roots: []RootArtifact{{ID: "src", Path: "stage/app", Digest: "d1", Signer: "builder"}},
			Receipts: []Receipt{{
				ID: "promote", Step: "promote", Signer: "releaser",
				Inputs:  []InputPort{{Path: "release/app", Digest: "d1", Consumption: Consume}},
				Outputs: []OutputPort{{Path: "final/app", Digest: "d2"}},
				Renames: []RenameRule{rule},
			}},
			Finals: []FinalArtifact{{Path: "final/app", Digest: "d2", Sensitive: true}},
		}
	}
	requireStatus(t, mustVerify(t, build(RenameRule{From: "stage/app", To: "release/app"})), StatusVerified)
	requireStatus(t, mustVerify(t, build(RenameRule{From: "stage/app", To: "release/other"})), StatusRejected)
	if _, err := Verify(build(RenameRule{From: "../stage/app", To: "release/app"})); err == nil {
		t.Fatal("expected tenant escape in rule to be rejected")
	}
}

func TestAlternativeImplementationsAreAtomic(t *testing.T) {
	makeSet := func(finalDebug bool) *ReceiptSet {
		finals := []FinalArtifact{{Path: "out/app", Digest: "sha256:app", Sensitive: true}}
		if finalDebug {
			finals = append(finals, FinalArtifact{Path: "out/debug", Digest: "sha256:debug", Sensitive: true})
		}
		return &ReceiptSet{
			Roots: []RootArtifact{
				{ID: "src-a", Path: "src/a", Digest: "sha256:a", Signer: "builder"},
				{ID: "src-b", Path: "src/b", Digest: "sha256:b", Signer: "builder"},
			},
			Receipts: []Receipt{
				{
					ID: "package-full", Step: "package", Signer: "packager",
					Inputs: []InputPort{{Path: "src/a", Digest: "sha256:a", Consumption: Consume}},
					Outputs: []OutputPort{
						{Path: "out/app", Digest: "sha256:app"},
						{Path: "out/debug", Digest: "sha256:debug"},
					},
				},
				{
					ID: "package-min", Step: "package", Signer: "packager",
					Inputs:  []InputPort{{Path: "src/b", Digest: "sha256:b", Consumption: Consume}},
					Outputs: []OutputPort{{Path: "out/app", Digest: "sha256:app"}},
				},
			},
			Finals: finals,
		}
	}

	t.Run("partial alternative cannot supply the missing output", func(t *testing.T) {
		set := makeSet(true)
		result := mustVerify(t, set)
		requireStatus(t, result, StatusVerified)
		rec := finalRecordsByIndex(result.Certificate)
		if !equalStrings(rec[0].Roots, []string{"src-a"}) || !equalStrings(rec[1].Roots, []string{"src-a"}) {
			t.Fatalf("outputs were mixed across alternatives: %+v", rec)
		}
	})

	t.Run("two complete alternatives give two attributions", func(t *testing.T) {
		set := makeSet(false)
		result := mustVerify(t, set)
		requireStatus(t, result, StatusAmbiguous)
		if result.Ambiguity == nil {
			t.Fatal("missing ambiguity")
		}
		got := []string{result.Ambiguity.First.Roots[0], result.Ambiguity.Second.Roots[0]}
		if !(got[0] == "src-a" && got[1] == "src-b") && !(got[0] == "src-b" && got[1] == "src-a") {
			t.Fatalf("unexpected alternatives: %v", got)
		}
	})
}

func TestLocallyValidStepsWithNoGlobalCorrespondence(t *testing.T) {
	set := &ReceiptSet{
		Receipts: []Receipt{
			{
				ID: "make-y", Step: "make-y", Signer: "builder",
				Inputs:  []InputPort{{Path: "x", Digest: "sha256:x", Consumption: Consume}},
				Outputs: []OutputPort{{Path: "y", Digest: "sha256:y"}},
			},
			{
				ID: "make-x", Step: "make-x", Signer: "builder",
				Inputs:  []InputPort{{Path: "y", Digest: "sha256:y", Consumption: Consume}},
				Outputs: []OutputPort{{Path: "x", Digest: "sha256:x"}},
			},
		},
		Finals: []FinalArtifact{{Path: "y", Digest: "sha256:y", Sensitive: true}},
	}

	result := mustVerify(t, set)
	requireStatus(t, result, StatusRejected)
	if len(result.Problems) == 0 || result.Problems[0].Code != ProblemNoConsistentBuild {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
}

func TestOutOfOrderAndEquivalentAlternatives(t *testing.T) {
	receipts := []Receipt{
		{
			ID: "build", Step: "build", Signer: "builder",
			Inputs:  []InputPort{{Path: "src", Digest: "sha256:src", Consumption: Share}},
			Outputs: []OutputPort{{Path: "app", Digest: "sha256:app"}},
		},
		{
			ID: "build-copy", Step: "build", Signer: "builder",
			Inputs:  []InputPort{{Path: "src", Digest: "sha256:src", Consumption: Share}},
			Outputs: []OutputPort{{Path: "app", Digest: "sha256:app"}},
		},
		{
			ID: "scan", Step: "scan", Signer: "scanner",
			Inputs:  []InputPort{{Path: "app", Digest: "sha256:app", Consumption: Share}},
			Outputs: []OutputPort{{Path: "report", Digest: "sha256:report"}},
		},
	}
	set := &ReceiptSet{
		Roots: []RootArtifact{{ID: "source", Path: "src", Digest: "sha256:src", Signer: "builder"}},
		Receipts: []Receipt{
			receipts[2], receipts[1], receipts[0],
		},
		Finals: []FinalArtifact{
			{Path: "app", Digest: "sha256:app", Sensitive: true},
			{Path: "report", Digest: "sha256:report", Sensitive: true},
		},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	for _, roots := range result.RootSets {
		if !equalStrings(roots.Roots, []string{"source"}) {
			t.Fatalf("unexpected roots: %+v", roots)
		}
	}
}

func TestCertificateRoundTripAndTamperDetection(t *testing.T) {
	set := &ReceiptSet{
		Roots: []RootArtifact{
			{ID: "root-a", Path: "src/a", Digest: "sha256:a", Signer: "builder"},
			{ID: "root-b", Path: "src/b", Digest: "sha256:b", Signer: "builder"},
		},
		Receipts: []Receipt{{
			ID: "merge", Step: "merge", Signer: "merger",
			Inputs: []InputPort{
				{Path: "src/a", Digest: "sha256:a", Consumption: Share},
				{Path: "src/b", Digest: "sha256:b", Consumption: Share},
			},
			Outputs: []OutputPort{{Path: "out/app", Digest: "sha256:app"}},
		}},
		Finals: []FinalArtifact{{Path: "out/app", Digest: "sha256:app", Sensitive: true}},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	cert := result.Certificate
	if err := VerifyCertificate(set, cert); err != nil {
		t.Fatalf("fresh certificate: %v", err)
	}
	if cert.Hash() != cert.BodyHash {
		t.Fatalf("Hash() = %s, BodyHash = %s", cert.Hash(), cert.BodyHash)
	}
	if !equalStrings(cert.Finals[0].Roots, []string{"root-a", "root-b"}) {
		t.Fatalf("unexpected merged roots: %v", cert.Finals[0].Roots)
	}

	data, err := json.Marshal(cert)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCertificate(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(set, decoded); err != nil {
		t.Fatalf("round-tripped certificate: %v", err)
	}

	t.Run("body tamper", func(t *testing.T) {
		tampered := *cert
		tampered.Edges = append([]Edge(nil), cert.Edges...)
		tampered.Edges[0].Source.RootID = "root-b"
		if err := VerifyCertificate(set, &tampered); err == nil || !strings.Contains(err.Error(), "body hash") {
			t.Fatalf("expected body hash failure, got %v", err)
		}
	})

	t.Run("structurally invalid but rehashed", func(t *testing.T) {
		tampered := *cert
		tampered.Edges = append([]Edge(nil), cert.Edges...)
		tampered.Edges[0].Source.RootID = "root-b"
		tampered.BodyHash = certificateHash(&tampered)
		if err := VerifyCertificate(set, &tampered); err == nil {
			t.Fatal("expected rehashed structurally invalid certificate to fail")
		}
	})

	t.Run("final source must carry the requested artifact", func(t *testing.T) {
		tampered := *cert
		tampered.Finals = append([]FinalRecord(nil), cert.Finals...)
		rootRecord := ArtifactRecord{}
		for _, rec := range cert.Artifacts {
			if rec.Instance.Kind == InstanceRoot && rec.Instance.RootID == "root-a" {
				rootRecord = rec
				break
			}
		}
		tampered.Finals[0].Instance = rootRecord.Instance
		tampered.Finals[0].Roots = append([]string(nil), rootRecord.Roots...)
		tampered.Finals[0].Receipts = append([]string(nil), rootRecord.Receipts...)
		tampered.Finals[0].Signers = append([]Role(nil), rootRecord.Signers...)
		tampered.BodyHash = certificateHash(&tampered)
		if err := VerifyCertificate(set, &tampered); err == nil {
			t.Fatal("expected mismatched final source to fail")
		}
	})
}

func TestRequiredSigners(t *testing.T) {
	set := &ReceiptSet{
		Roots: []RootArtifact{{ID: "root", Path: "src", Digest: "sha256:src", Signer: "builder"}},
		Receipts: []Receipt{{
			ID: "release", Step: "release", Signer: "releaser",
			Inputs:  []InputPort{{Path: "src", Digest: "sha256:src", Consumption: Consume}},
			Outputs: []OutputPort{{Path: "app", Digest: "sha256:app"}},
		}},
		Finals: []FinalArtifact{{
			Path: "app", Digest: "sha256:app", Sensitive: true,
			RequiredSigners: []Role{"releaser"},
		}},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	if err := VerifyCertificate(set, result.Certificate); err != nil {
		t.Fatalf("certificate: %v", err)
	}

	set.Finals[0].RequiredSigners = []Role{"auditor"}
	result = mustVerify(t, set)
	requireStatus(t, result, StatusRejected)
}

func TestSignerPolicyFiltersRootsAndReceipts(t *testing.T) {
	set := &ReceiptSet{
		Roots: []RootArtifact{{ID: "root", Path: "src", Digest: "d1", Signer: "builder"}},
		Receipts: []Receipt{{
			ID: "release", Step: "release", Signer: "releaser",
			Inputs:  []InputPort{{Path: "src", Digest: "d1", Consumption: Consume}},
			Outputs: []OutputPort{{Path: "app", Digest: "d2"}},
		}},
		Finals: []FinalArtifact{{Path: "app", Digest: "d2", Sensitive: true}},
	}
	set.Policy.AllowedSigners = []Role{"releaser"}
	requireStatus(t, mustVerify(t, set), StatusRejected)

	set.Policy.AllowedSigners = []Role{"builder", "releaser"}
	requireStatus(t, mustVerify(t, set), StatusVerified)
}

func TestBoundsAndRuleValidation(t *testing.T) {
	t.Run("too many steps", func(t *testing.T) {
		set := &ReceiptSet{
			Roots:  []RootArtifact{{ID: "root", Path: "src", Digest: "d", Signer: "s"}},
			Finals: []FinalArtifact{{Path: "out", Digest: "d"}},
		}
		for i := 0; i < maxSteps+1; i++ {
			set.Receipts = append(set.Receipts, Receipt{
				ID: "r" + string(rune('a'+i)), Step: "s" + string(rune('a'+i)), Signer: "s",
				Inputs:  []InputPort{{Path: "src", Digest: "d", Consumption: Share}},
				Outputs: []OutputPort{{Path: "out", Digest: "d"}},
			})
		}
		if _, err := Verify(set); err == nil {
			t.Fatal("expected step limit rejection")
		}
	})

	t.Run("too many candidates", func(t *testing.T) {
		set := &ReceiptSet{
			Roots:  []RootArtifact{{ID: "root", Path: "src", Digest: "d", Signer: "s"}},
			Finals: []FinalArtifact{{Path: "out", Digest: "d"}},
		}
		for i := 0; i < maxCandidatesPerStep+1; i++ {
			set.Receipts = append(set.Receipts, Receipt{
				ID: "r" + string(rune('a'+i)), Step: "same", Signer: "s",
				Inputs:  []InputPort{{Path: "src", Digest: "d", Consumption: Share}},
				Outputs: []OutputPort{{Path: "out", Digest: "d"}},
			})
		}
		if _, err := Verify(set); err == nil {
			t.Fatal("expected candidate limit rejection")
		}
	})

	t.Run("two captures", func(t *testing.T) {
		_, err := compileRule(RenameRule{From: "{a}/{b}", To: "{a}/{b}"})
		if err == nil {
			t.Fatal("expected multi-capture rule rejection")
		}
	})
}

func TestSixteenStepsThreeSemanticallyEquivalentCandidates(t *testing.T) {
	set := &ReceiptSet{
		Roots:  []RootArtifact{{ID: "root", Path: "p0", Digest: "d0", Signer: "builder"}},
		Finals: []FinalArtifact{{Path: "p16", Digest: "d16", Sensitive: true}},
	}
	for i := 1; i <= 16; i++ {
		inPath := "p" + itoa(i-1)
		outPath := "p" + itoa(i)
		inDigest := "d" + itoa(i-1)
		outDigest := "d" + itoa(i)
		step := "s" + itoa(i)
		for j := 0; j < 3; j++ {
			set.Receipts = append(set.Receipts, Receipt{
				ID: step + "-alt-" + itoa(j), Step: step, Signer: "builder",
				Inputs:  []InputPort{{Path: inPath, Digest: inDigest, Consumption: Share}},
				Outputs: []OutputPort{{Path: outPath, Digest: outDigest}},
			})
		}
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	if len(result.Certificate.Selections) != 16 {
		t.Fatalf("selections = %d", len(result.Certificate.Selections))
	}
	if !equalStrings(result.RootSets[0].Roots, []string{"root"}) {
		t.Fatalf("roots = %v", result.RootSets[0].Roots)
	}
}

func TestSourceAlternativesRemainDistinct(t *testing.T) {
	set := &ReceiptSet{
		Receipts: []Receipt{
			{ID: "build-a", Step: "build", Signer: "builder", Outputs: []OutputPort{{Path: "app", Digest: "d"}}},
			{ID: "build-b", Step: "build", Signer: "builder", Outputs: []OutputPort{{Path: "app", Digest: "d"}}},
		},
		Finals: []FinalArtifact{{Path: "app", Digest: "d", Sensitive: true}},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusAmbiguous)
	roots := []string{result.Ambiguity.First.Roots[0], result.Ambiguity.Second.Roots[0]}
	if !(roots[0] == "receipt:build-a" && roots[1] == "receipt:build-b") &&
		!(roots[0] == "receipt:build-b" && roots[1] == "receipt:build-a") {
		t.Fatalf("unexpected source roots: %v", roots)
	}
}

func TestRootOnlyFinal(t *testing.T) {
	set := &ReceiptSet{
		Roots:  []RootArtifact{{ID: "clean", Path: "app", Digest: "d", Signer: "builder"}},
		Finals: []FinalArtifact{{Path: "app", Digest: "d", Sensitive: true}},
	}
	result := mustVerify(t, set)
	requireStatus(t, result, StatusVerified)
	if err := VerifyCertificate(set, result.Certificate); err != nil {
		t.Fatalf("certificate: %v", err)
	}
	if !equalStrings(result.Certificate.Finals[0].Roots, []string{"clean"}) {
		t.Fatalf("roots = %v", result.Certificate.Finals[0].Roots)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
