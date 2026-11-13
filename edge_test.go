package netsec

import "testing"

func TestSymlinkDepthAndChainSemantics(t *testing.T) {
	t.Run("chain staying inside root resolves", func(t *testing.T) {
		files := []FileInfo{
			{RepoPath: "a", IsSymlink: true, LinkTarget: "b"},
			{RepoPath: "b", IsSymlink: true, LinkTarget: "c"},
			{RepoPath: "c", IsSymlink: true, LinkTarget: "real.yaml"},
			{RepoPath: "real.yaml"},
		}
		admitter := NewAdmitter(testCatalog(t, files), nil)
		err := admitter.Validate(&Kustomization{Resources: []string{"a"}})
		assertAllowed(t, err)
	})

	t.Run("relative parent chain escaping root rejected", func(t *testing.T) {
		files := []FileInfo{
			{RepoPath: "dir/link", IsSymlink: true, LinkTarget: "../../escape.yaml"},
		}
		admitter := NewAdmitter(testCatalog(t, files), nil)
		err := admitter.Validate(&Kustomization{Resources: []string{"dir/link"}})
		assertViolation(t, err, ReasonSymlinkEscapesRoot)
	})

	t.Run("nested symlink prefixes re-expanded", func(t *testing.T) {
		files := []FileInfo{
			{RepoPath: "a", IsSymlink: true, LinkTarget: "b/file.yaml"},
			{RepoPath: "b", IsSymlink: true, LinkTarget: "real"},
			{RepoPath: "real/file.yaml"},
		}
		catalog := testCatalog(t, files)
		resolved, reason, _ := resolveLocalPath(catalog, "", "a")
		if reason != "" || resolved != "real/file.yaml" {
			t.Fatalf("expected real/file.yaml, got %q reason %q", resolved, reason)
		}
	})

	t.Run("deep chain reported as depth violation", func(t *testing.T) {
		files := make([]FileInfo, 0, maxSymlinkHops+2)
		for index := 0; index < maxSymlinkHops+1; index++ {
			files = append(files, FileInfo{
				RepoPath:   "l" + itoa(index),
				IsSymlink:  true,
				LinkTarget: "l" + itoa(index+1),
			})
		}
		files = append(files, FileInfo{RepoPath: "l" + itoa(maxSymlinkHops+1)})
		admitter := NewAdmitter(testCatalog(t, files), nil)
		err := admitter.Validate(&Kustomization{Resources: []string{"l0"}})
		assertViolation(t, err, ReasonSymlinkTooDeep)
	})
}

func TestNilCatalogAllowsLexicalContainmentOnly(t *testing.T) {
	admitter := NewAdmitter(nil, nil)

	err := admitter.Validate(&Kustomization{
		Resources: []string{"config/app.yaml", "./deploy.yaml"},
	})
	assertAllowed(t, err)

	err = admitter.Validate(&Kustomization{
		Resources: []string{"../outside.yaml"},
	})
	assertViolation(t, err, ReasonPathEscapesRoot)
}

func TestNilKustomizationRejected(t *testing.T) {
	admitter := NewAdmitter(nil, nil)
	err := admitter.Validate(nil)
	assertViolation(t, err, ReasonEmptyPath)
}

func TestBackslashTraversalRejected(t *testing.T) {
	admitter := NewAdmitter(nil, nil)
	err := admitter.Validate(&Kustomization{
		Resources: []string{`..\..\windows\system32`},
	})
	assertViolation(t, err, ReasonPathEscapesRoot)
}

func TestViolationAggregation(t *testing.T) {
	admitter := NewAdmitter(nil, []string{"good.example.com"})
	err := admitter.Validate(&Kustomization{
		Resources: []string{
			"../escape-a.yaml",
			"https://evil.example.com/r?ref=" + pinnedCommit,
			"https://good.example.com/r?ref=branch",
		},
	})
	var admissionErr *AdmissionError
	if !asAdmissionError(err, &admissionErr) {
		t.Fatalf("expected AdmissionError, got %v", err)
	}
	if len(admissionErr.Violations) != 3 {
		t.Fatalf("expected 3 aggregated violations, got %d: %+v",
			len(admissionErr.Violations), admissionErr.Violations)
	}
	for _, reason := range []Reason{
		ReasonPathEscapesRoot,
		ReasonHostNotAllowed,
		ReasonRefNotCommit,
	} {
		if !admissionErr.HasReason(reason) {
			t.Fatalf("missing expected reason %q in %+v", reason, admissionErr.Violations)
		}
	}
}
