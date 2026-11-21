package vex

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func signedID(compiler, digest, config string) ObjectID {
	return ObjectID{Compiler: compiler, SourceDigest: digest, BuildConfig: config}
}

func testObject(id ObjectID, refs []Reference, symbols ...Symbol) Object {
	return Object{
		ID:         id,
		References: refs,
		Symbols:    symbols,
		Signature:  Signature{Signed: true},
	}
}

func testManifest(config string, entries []string, groups ...ArchiveGroup) LinkManifest {
	return LinkManifest{
		Config:        config,
		EntrySymbols:  entries,
		ArchiveGroups: groups,
		Signature:     Signature{Signed: true},
	}
}

func testConfig(id string, tags []string) CandidateConfig {
	return CandidateConfig{
		ID:        id,
		Allowed:   true,
		Tags:      tags,
		Signature: Signature{Signed: true},
	}
}

func analyzeOne(t *testing.T, evidence Evidence) *Certificate {
	t.Helper()
	cert, err := Analyze(evidence)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return cert
}

func requireVerdict(t *testing.T, cert *Certificate, want Verdict) {
	t.Helper()
	if cert.Verdict != want {
		t.Fatalf("verdict = %s, want %s\nreason: %s\ncertificate: %#v", cert.Verdict, want, cert.Reason, cert)
	}
}

func TestUnreachableDependencyInArchiveIsNotAffected(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main-src", config)
	vulnerableID := signedID("gc", "vulnerable-src", config)
	evidence := Evidence{
		Marked:         []ObjectID{vulnerableID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, []string{"linux", "amd64"})},
		Manifests: []LinkManifest{testManifest(config, []string{"main"}, ArchiveGroup{
			ID: "archive",
			Members: []ObjectID{
				mainID,
				vulnerableID,
			},
		})},
		Summaries: []Object{
			testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong}),
			testObject(vulnerableID, nil, Symbol{Name: "vulnerable", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	proof := cert.Configs[0]
	if len(proof.Extracted) != 1 || idKey(proof.Extracted[0]) != idKey(mainID) {
		t.Fatalf("extraction = %#v, want only main object", proof.Extracted)
	}
	if len(proof.Bindings) != 1 || len(proof.Bindings[0].Reachable) != 1 {
		t.Fatalf("bindings = %#v, want one reachability witness for main", proof.Bindings)
	}
}

func TestStrongDefinitionReplacesSameNamedWeakDefinition(t *testing.T) {
	config := "linux/amd64"
	weakID := signedID("gc", "old-vulnerable", config)
	strongID := signedID("gc", "fixed", config)
	evidence := Evidence{
		Marked:         []ObjectID{weakID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"f"}, ArchiveGroup{
			Members: []ObjectID{weakID, strongID},
		})},
		Summaries: []Object{
			testObject(weakID, nil, Symbol{Name: "f", Binding: BindingWeak}),
			testObject(strongID, nil, Symbol{Name: "f", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	if len(cert.Configs[0].Bindings) != 1 {
		t.Fatalf("bindings = %#v, want strong binding only", cert.Configs[0].Bindings)
	}
	if got := cert.Configs[0].Bindings[0].Bindings[0].Object; idKey(got) != idKey(strongID) {
		t.Fatalf("bound object = %#v, want strong %#v", got, strongID)
	}
}

func TestEveryWeakBindingIsConsidered(t *testing.T) {
	config := "linux/amd64"
	fixedID := signedID("gc", "fixed", config)
	vulnerableID := signedID("gc", "vulnerable", config)
	evidence := Evidence{
		Marked:         []ObjectID{vulnerableID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"f"}, ArchiveGroup{
			Members: []ObjectID{fixedID, vulnerableID},
		})},
		Summaries: []Object{
			testObject(fixedID, nil, Symbol{Name: "f", Binding: BindingWeak}),
			testObject(vulnerableID, nil, Symbol{Name: "f", Binding: BindingWeak}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Affected)
	if len(cert.Configs[0].Bindings) != 2 {
		t.Fatalf("bindings = %d, want both weak choices", len(cert.Configs[0].Bindings))
	}
}

func TestChooseOneGroupIsCorrelatedAcrossSymbols(t *testing.T) {
	config := "linux/amd64"
	aID := signedID("gc", "alternative-a", config)
	bID := signedID("gc", "alternative-b", config)
	vulnerableID := signedID("gc", "vulnerable", config)

	a := testObject(aID, nil,
		Symbol{Name: "f", Binding: BindingGroup, Group: "implementation"},
		Symbol{Name: "g", Binding: BindingGroup, Group: "implementation"},
	)
	b := testObject(bID, nil,
		Symbol{Name: "f", Binding: BindingGroup, Group: "implementation", References: []Reference{{Symbol: "vulnerable"}}},
		Symbol{Name: "g", Binding: BindingGroup, Group: "implementation"},
	)
	vulnerable := testObject(vulnerableID, nil, Symbol{Name: "vulnerable", Binding: BindingStrong})

	evidence := Evidence{
		Marked:         []ObjectID{vulnerableID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"f", "g"}, ArchiveGroup{
			Members: []ObjectID{aID, bID, vulnerableID},
		})},
		Summaries: []Object{a, b, vulnerable},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Affected)
	if len(cert.Configs[0].Bindings) != 2 {
		t.Fatalf("bindings = %d, want one per group alternative", len(cert.Configs[0].Bindings))
	}
	for _, binding := range cert.Configs[0].Bindings {
		var fObject, gObject ObjectID
		for _, choice := range binding.Bindings {
			switch choice.Symbol {
			case "f":
				fObject = choice.Object
			case "g":
				gObject = choice.Object
			}
		}
		if fObject != gObject {
			t.Fatalf("group choice was split per symbol: f=%v g=%v", fObject, gObject)
		}
	}
}

func TestCyclicReferencesTerminate(t *testing.T) {
	config := "linux/amd64"
	aID := signedID("gc", "a", config)
	bID := signedID("gc", "b", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"a"}, ArchiveGroup{
			Members: []ObjectID{aID, bID},
		})},
		Summaries: []Object{
			testObject(aID, nil, Symbol{Name: "a", Binding: BindingStrong, References: []Reference{{Symbol: "b"}}}),
			testObject(bID, nil, Symbol{Name: "b", Binding: BindingStrong, References: []Reference{{Symbol: "a"}}}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	if len(cert.Configs[0].Extracted) != 2 {
		t.Fatalf("extraction = %#v, want the complete cycle", cert.Configs[0].Extracted)
	}
}

func TestConditionalReferenceOnlyAppearsInSelectedConfiguration(t *testing.T) {
	linux := "linux/amd64"
	darwin := "darwin/arm64"
	mainLinux := signedID("gc", "main", linux)
	vulnerableLinux := signedID("gc", "vulnerable", linux)
	mainDarwin := signedID("gc", "main", darwin)
	vulnerableDarwin := signedID("gc", "vulnerable", darwin)
	evidence := Evidence{
		Marked:         []ObjectID{vulnerableLinux, vulnerableDarwin},
		AllowedConfigs: []string{linux, darwin},
		Configs: []CandidateConfig{
			testConfig(linux, []string{"linux", "amd64"}),
			testConfig(darwin, []string{"darwin", "arm64"}),
		},
		Manifests: []LinkManifest{
			testManifest(linux, []string{"main"}, ArchiveGroup{Members: []ObjectID{mainLinux, vulnerableLinux}}),
			testManifest(darwin, []string{"main"}, ArchiveGroup{Members: []ObjectID{mainDarwin, vulnerableDarwin}}),
		},
		Summaries: []Object{
			testObject(mainLinux, nil, Symbol{Name: "main", Binding: BindingStrong, References: []Reference{{Symbol: "vulnerable", Condition: "linux && amd64"}}}),
			testObject(vulnerableLinux, nil, Symbol{Name: "vulnerable", Binding: BindingStrong}),
			testObject(mainDarwin, nil, Symbol{Name: "main", Binding: BindingStrong, References: []Reference{{Symbol: "vulnerable", Condition: "linux && amd64"}}}),
			testObject(vulnerableDarwin, nil, Symbol{Name: "vulnerable", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Affected)
	if len(cert.Configs) != 2 {
		t.Fatalf("config proofs = %d, want 2", len(cert.Configs))
	}
	if cert.Configs[0].Config == linux && cert.Configs[0].Verdict != ConfigAffected {
		t.Fatalf("linux proof = %s, want affected", cert.Configs[0].Verdict)
	}
	if cert.Configs[1].Config == darwin && cert.Configs[1].Verdict != ConfigNotAffected {
		t.Fatalf("darwin proof = %s, want not affected", cert.Configs[1].Verdict)
	}
}

func TestIndirectReferenceIntroducedByLinkedObject(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	vulnerableID := signedID("gc", "vulnerable", config)
	evidence := Evidence{
		Marked:         []ObjectID{vulnerableID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"main"}, ArchiveGroup{
			Members: []ObjectID{mainID, vulnerableID},
		})},
		Summaries: []Object{
			testObject(mainID, nil, Symbol{
				Name:    "main",
				Binding: BindingStrong,
				References: []Reference{{
					Symbol:   "vulnerable",
					Indirect: true,
				}},
			}),
			testObject(vulnerableID, nil, Symbol{Name: "vulnerable", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Affected)
}

func TestMissingSignedSummaryIsUnknown(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	missingID := signedID("gc", "missing", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"main"}, ArchiveGroup{
			Members: []ObjectID{mainID, missingID},
		})},
		Summaries: []Object{
			testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong, References: []Reference{{Symbol: "missing"}}}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Unknown)
	if cert.Configs[0].EvidenceComplete {
		t.Fatalf("proof claims complete evidence despite a missing signed summary")
	}
}

func TestOneAllowedConfigurationWithMissingEvidenceForcesUnknown(t *testing.T) {
	good := "linux/amd64"
	missing := "linux/arm64"
	goodMain := signedID("gc", "good-main", good)
	evidence := Evidence{
		AllowedConfigs: []string{good, missing},
		Configs: []CandidateConfig{
			testConfig(good, nil),
			testConfig(missing, nil),
		},
		Manifests: []LinkManifest{
			testManifest(good, []string{"main"}),
			{Config: missing, EntrySymbols: []string{"main"}, Signature: Signature{Signed: true}},
		},
		Summaries: []Object{
			testObject(goodMain, nil, Symbol{Name: "main", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Unknown)
}

func TestSignedUnlinkableConfigurationDoesNotRequireLinkManifest(t *testing.T) {
	config := "linux/amd64"
	candidate := testConfig(config, nil)
	candidate.Linkable = false
	candidate.LinkabilityKnown = true
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{candidate},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	if cert.Configs[0].Verdict != ConfigNotLinkable {
		t.Fatalf("config verdict = %s, want not_linkable", cert.Configs[0].Verdict)
	}
}

func TestDeclaredLinkableButUnresolvedEntryIsUnknown(t *testing.T) {
	config := "linux/amd64"
	candidate := testConfig(config, nil)
	candidate.Linkable = true
	candidate.LinkabilityKnown = true
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{candidate},
		Manifests:      []LinkManifest{testManifest(config, []string{"missing"})},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Unknown)
}

func TestUnsignedObjectIsUnknown(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	obj := testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong})
	obj.Signature = nil
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests:      []LinkManifest{testManifest(config, []string{"main"})},
		Summaries:      []Object{obj},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Unknown)
}

func TestSignatureVerifierControlsTrust(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests:      []LinkManifest{testManifest(config, []string{"main"})},
		Summaries:      []Object{testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong})},
	}
	reject := SignatureVerifierFunc(func([]byte, any) error { return errors.New("bad signature") })
	cert := analyzeOne(t, evidence)
	// The embedded signatures are structurally present without a verifier.
	requireVerdict(t, cert, NotAffected)

	cert, err := Analyze(evidence, Options{Verifier: reject})
	if err != nil {
		t.Fatal(err)
	}
	requireVerdict(t, cert, Unknown)
}

func TestSequentialArchiveGroupsResolveReferencesIntroducedByEarlierGroups(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	dependencyID := signedID("gc", "dependency", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"main"},
			ArchiveGroup{ID: "first", Members: []ObjectID{mainID}},
			ArchiveGroup{ID: "second", Members: []ObjectID{dependencyID}},
		)},
		Summaries: []Object{
			testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong, References: []Reference{{Symbol: "dependency"}}}),
			testObject(dependencyID, nil, Symbol{Name: "dependency", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	if len(cert.Configs[0].Extracted) != 2 {
		t.Fatalf("extracted = %#v, want both sequential groups", cert.Configs[0].Extracted)
	}
}

func TestGroupChoiceCanSelectMarkedAlternative(t *testing.T) {
	config := "linux/amd64"
	fixedID := signedID("gc", "fixed", config)
	vulnerableID := signedID("gc", "vulnerable", config)
	fixed := testObject(fixedID, nil, Symbol{Name: "f", Binding: BindingGroup, Group: "choose"})
	vulnerable := testObject(vulnerableID, nil, Symbol{Name: "f", Binding: BindingGroup, Group: "choose"})
	evidence := Evidence{
		Marked:         []ObjectID{vulnerableID},
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests: []LinkManifest{testManifest(config, []string{"f"}, ArchiveGroup{
			Members: []ObjectID{fixedID, vulnerableID},
		})},
		Summaries: []Object{fixed, vulnerable},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Affected)
	if len(cert.Configs[0].Bindings) != 2 {
		t.Fatalf("bindings = %d, want both group alternatives", len(cert.Configs[0].Bindings))
	}
	for _, binding := range cert.Configs[0].Bindings {
		if len(binding.Groups) != 1 || binding.Groups[0].Group != "choose" {
			t.Fatalf("group choices = %#v", binding.Groups)
		}
	}
}

func TestSharedArchiveMemberEvidenceAcrossConfigurations(t *testing.T) {
	aConfig := "linux/amd64"
	bConfig := "linux/arm64"
	sharedID := signedID("gc", "shared-main", "shared")
	aMain := signedID("gc", "shared-main", aConfig)
	bMain := signedID("gc", "shared-main", bConfig)
	evidence := Evidence{
		AllowedConfigs: []string{aConfig, bConfig},
		Configs: []CandidateConfig{
			testConfig(aConfig, []string{"linux", "amd64"}),
			testConfig(bConfig, []string{"linux", "arm64"}),
		},
		Manifests: []LinkManifest{
			testManifest(aConfig, []string{"main"}, ArchiveGroup{Members: []ObjectID{aMain}}),
			testManifest(bConfig, []string{"main"}, ArchiveGroup{Members: []ObjectID{bMain}}),
		},
		Summaries: []Object{
			testObject(sharedID, nil, Symbol{Name: "main", Binding: BindingStrong}),
		},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, NotAffected)
	if len(cert.Configs) != 2 {
		t.Fatalf("config proofs = %d, want 2", len(cert.Configs))
	}
}

func TestCertificateReplaysAndDetectsTampering(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests:      []LinkManifest{testManifest(config, []string{"main"})},
		Summaries:      []Object{testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong})},
	}
	cert := analyzeOne(t, evidence)
	if err := VerifyCertificate(cert, evidence); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	tampered := *cert
	tampered.Verdict = Affected
	if err := VerifyCertificate(&tampered, evidence); err == nil {
		t.Fatal("tampered certificate was accepted")
	}

	data, err := json.Marshal(cert)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Certificate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(&decoded, evidence); err != nil {
		t.Fatalf("JSON certificate replay: %v", err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, nil)},
		Manifests:      []LinkManifest{testManifest(config, []string{"main"})},
		Summaries:      []Object{testObject(mainID, nil, Symbol{Name: "main", Binding: BindingStrong})},
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := Analyze(data)
	if err != nil {
		t.Fatal(err)
	}
	requireVerdict(t, cert, NotAffected)
}

func TestConditionParserRejectsMalformedExpression(t *testing.T) {
	config := "linux/amd64"
	mainID := signedID("gc", "main", config)
	evidence := Evidence{
		AllowedConfigs: []string{config},
		Configs:        []CandidateConfig{testConfig(config, []string{"linux"})},
		Manifests:      []LinkManifest{testManifest(config, []string{"main"})},
		Summaries: []Object{testObject(mainID, nil, Symbol{
			Name:       "main",
			Binding:    BindingStrong,
			References: []Reference{{Symbol: "missing", Condition: "linux &&"}},
		})},
	}
	cert := analyzeOne(t, evidence)
	requireVerdict(t, cert, Unknown)
	if !strings.Contains(strings.Join(cert.Configs[0].MissingEvidence, " "), "condition") {
		t.Fatalf("missing evidence = %#v, want condition error", cert.Configs[0].MissingEvidence)
	}
}
