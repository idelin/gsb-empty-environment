package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mustDigest(t *testing.T, value string) Digest {
	t.Helper()
	digest, err := ParseDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func grant(t *testing.T, policy *Policy, digest Digest, actions ...Action) {
	t.Helper()
	if err := policy.Grant(digest, actions...); err != nil {
		t.Fatal(err)
	}
}

func registerTool(t *testing.T, catalog *Catalog, id string) {
	t.Helper()
	if err := catalog.RegisterTool(id, "fixture tool"); err != nil {
		t.Fatal(err)
	}
}

func registerEndpoint(t *testing.T, catalog *Catalog, id string) {
	t.Helper()
	if err := catalog.RegisterEndpoint(id, "fixture endpoint"); err != nil {
		t.Fatal(err)
	}
}

func registerEnv(t *testing.T, catalog *Catalog, name, value string) {
	t.Helper()
	if err := catalog.RegisterEnvironment(name, value); err != nil {
		t.Fatal(err)
	}
}

func findingFor(t *testing.T, result *Result, digest Digest, code Code) Violation {
	t.Helper()
	matches := result.ViolationsFor(digest)
	for _, finding := range matches {
		if finding.Code == code {
			return finding
		}
	}
	t.Fatalf("package %s has no %s finding; findings=%+v", digest, code, matches)
	return Violation{}
}

func TestPlanEmitsExactPerPackageCapabilities(t *testing.T) {
	a := mustDigest(t, "pkg-a")
	b := mustDigest(t, "pkg-b")
	catalog := NewCatalog()
	registerTool(t, catalog, "go")
	registerEndpoint(t, catalog, "registry")
	registerEnv(t, catalog, "TOKEN", "super-secret-value")

	policy := NewPolicy()
	grant(t, policy, a,
		Exec("go"),
		Connect("registry"),
		Env("TOKEN"),
		Read("src/input"),
	)
	grant(t, policy, a,
		Exec("golang"),
		Connect("registry-alias"),
		Env("TOKEN_ALIAS"),
	)

	graph := NewPackageGraph(
		PackageNode{
			Digest: a,
			Scripts: []ScriptDeclaration{{
				Name: "build",
				Actions: []Action{
					Read("src/input"),
					Write("out/result"),
					Exec("go"),
					Exec("golang"),
					Connect("registry"),
					Connect("registry-alias"),
					Env("TOKEN"),
					Env("TOKEN_ALIAS"),
				},
			}},
		},
		PackageNode{Digest: b, DependsOn: []Digest{a}, Scripts: []ScriptDeclaration{{Name: "package"}}},
	)
	host, err := NewHost(HostConfig{Policy: policy, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := host.Plan(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Order) != 2 || plan.Order[0] != a || plan.Order[1] != b {
		t.Fatalf("unexpected dependency order: %v", plan.Order)
	}
	manifest, ok := plan.Manifest(a)
	if !ok {
		t.Fatal("missing package manifest")
	}
	granted := NewActionSet(manifest.Granted...)
	for _, action := range []Action{Read("src/input"), Exec("go"), Connect("registry"), Env("TOKEN")} {
		if !granted.Has(action) {
			t.Fatalf("%s should be granted; got %s", action, granted.List())
		}
	}
	denied := NewActionSet(manifest.Denied...)
	for _, action := range []Action{
		Write("out/result"),
		Exec("golang"),
		Connect("registry-alias"),
		Env("TOKEN_ALIAS"),
	} {
		if !denied.Has(action) {
			t.Fatalf("%s should be denied; got %s", action, denied.List())
		}
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planJSON), "super-secret-value") {
		t.Fatal("plan leaked an environment value")
	}
}

func TestMaliciousReadTracesReportCredentialPathRefusals(t *testing.T) {
	absolute := mustDigest(t, "pkg-absolute-read")
	dot := mustDigest(t, "pkg-dot-read")
	symlinkParent := mustDigest(t, "pkg-symlink-parent")
	symlinkFinal := mustDigest(t, "pkg-symlink-final")

	policy := NewPolicy()
	grant(t, policy, symlinkParent, Read("secrets/credentials"))
	grant(t, policy, symlinkFinal, Read("credentials-link"))

	files := NewVirtualFS()
	if err := files.AddSourceSymlink(symlinkParent, "secrets", "/host/.aws/credentials"); err != nil {
		t.Fatal(err)
	}
	if err := files.AddSourceSymlink(symlinkFinal, "credentials-link", "/host/.aws/credentials"); err != nil {
		t.Fatal(err)
	}

	graph := NewPackageGraph(
		PackageNode{
			Digest: absolute,
			Scripts: []ScriptDeclaration{{
				Name:    "steal",
				Actions: []Action{Read("/host/.aws/credentials")},
			}},
		},
		PackageNode{
			Digest: dot,
			Scripts: []ScriptDeclaration{{
				Name:    "steal",
				Actions: []Action{Read("../other-package/credentials")},
			}},
		},
		PackageNode{
			Digest: symlinkParent,
			Scripts: []ScriptDeclaration{{
				Name:    "steal",
				Actions: []Action{Read("secrets/credentials")},
			}},
		},
		PackageNode{
			Digest: symlinkFinal,
			Scripts: []ScriptDeclaration{{
				Name:    "steal",
				Actions: []Action{Read("credentials-link")},
			}},
		},
	)
	host, err := NewHost(HostConfig{Policy: policy, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	runners := Runners{}
	paths := map[Digest]string{
		absolute:      "/host/.aws/credentials",
		dot:           "../other-package/credentials",
		symlinkParent: "secrets/credentials",
		symlinkFinal:  "credentials-link",
	}
	for digest, path := range paths {
		digest, path := digest, path
		runners[ScriptRef{Package: digest, Name: "steal"}] = func(_ context.Context, execution *Execution) error {
			_, err := execution.Read(path)
			return err
		}
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), runners)
	if err != nil {
		t.Fatal(err)
	}
	if result.Visible(absolute) || result.Visible(dot) || result.Visible(symlinkParent) || result.Visible(symlinkFinal) {
		t.Fatal("a malicious package became visible")
	}
	findingFor(t, result, absolute, CodeAbsolutePath)
	findingFor(t, result, dot, CodeDotSegment)
	parentFinding := findingFor(t, result, symlinkParent, CodeSymlinkParent)
	if parentFinding.Action != Read("secrets/credentials") || len(parentFinding.Process) != 1 || parentFinding.Process[0] != "steal" {
		t.Fatalf("bad package-to-action evidence: %+v", parentFinding)
	}
	findingFor(t, result, symlinkFinal, CodeSymlinkTarget)
}

func TestMaliciousWriteTracesCannotEscapePackageOutputRoot(t *testing.T) {
	absolute := mustDigest(t, "pkg-absolute-write")
	dot := mustDigest(t, "pkg-dot-write")
	symlinkParent := mustDigest(t, "pkg-symlink-parent-write")
	symlinkFinal := mustDigest(t, "pkg-symlink-final-write")

	policy := NewPolicy()
	grant(t, policy, symlinkParent, Write("escape/installed"))
	grant(t, policy, symlinkFinal, Write("output-link"))

	files := NewVirtualFS()
	if err := files.AddOutputSymlink(symlinkParent, "escape", "/install-root"); err != nil {
		t.Fatal(err)
	}
	if err := files.AddOutputSymlink(symlinkFinal, "output-link", "/install-root/installed"); err != nil {
		t.Fatal(err)
	}

	graph := NewPackageGraph(
		PackageNode{Digest: absolute, Scripts: []ScriptDeclaration{{
			Name: "install", Actions: []Action{Write("/install-root/installed")},
		}}},
		PackageNode{Digest: dot, Scripts: []ScriptDeclaration{{
			Name: "install", Actions: []Action{Write("../other-package/installed")},
		}}},
		PackageNode{Digest: symlinkParent, Scripts: []ScriptDeclaration{{
			Name: "install", Actions: []Action{Write("escape/installed")},
		}}},
		PackageNode{Digest: symlinkFinal, Scripts: []ScriptDeclaration{{
			Name: "install", Actions: []Action{Write("output-link")},
		}}},
	)
	host, err := NewHost(HostConfig{Policy: policy, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	runners := Runners{}
	paths := map[Digest]string{
		absolute:      "/install-root/installed",
		dot:           "../other-package/installed",
		symlinkParent: "escape/installed",
		symlinkFinal:  "output-link",
	}
	for digest, path := range paths {
		digest, path := digest, path
		runners[ScriptRef{Package: digest, Name: "install"}] = func(_ context.Context, execution *Execution) error {
			return execution.Write(path, []byte("malicious"))
		}
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), runners)
	if err != nil {
		t.Fatal(err)
	}
	findingFor(t, result, absolute, CodeAbsolutePath)
	findingFor(t, result, dot, CodeDotSegment)
	findingFor(t, result, symlinkParent, CodeSymlinkParent)
	findingFor(t, result, symlinkFinal, CodeSymlinkTarget)
	if len(result.Published) != 0 {
		t.Fatalf("malicious writes became visible: %+v", result.Published)
	}
}

func TestToolEndpointAndEnvAliasesAreRejectedExactly(t *testing.T) {
	toolAlias := mustDigest(t, "pkg-tool-alias")
	endpointAlias := mustDigest(t, "pkg-endpoint-alias")
	envAlias := mustDigest(t, "pkg-env-alias")
	secret := "do-not-copy-this-secret"

	policy := NewPolicy()
	grant(t, policy, toolAlias, Exec("go"), Exec("go-alias"))
	grant(t, policy, endpointAlias, Connect("registry"), Connect("registry-alias"))
	grant(t, policy, envAlias, Env("TOKEN"), Env("TOKEN_ALIAS"))

	catalog := NewCatalog()
	registerTool(t, catalog, "go")
	registerEndpoint(t, catalog, "registry")
	registerEnv(t, catalog, "TOKEN", secret)

	graph := NewPackageGraph(
		PackageNode{Digest: toolAlias, Scripts: []ScriptDeclaration{{
			Name: "build", Actions: []Action{Exec("go"), Exec("go-alias")},
		}}},
		PackageNode{Digest: endpointAlias, Scripts: []ScriptDeclaration{{
			Name: "build", Actions: []Action{Connect("registry"), Connect("registry-alias")},
		}}},
		PackageNode{Digest: envAlias, Scripts: []ScriptDeclaration{{
			Name: "build", Actions: []Action{Env("TOKEN"), Env("TOKEN_ALIAS")},
		}}},
	)
	host, err := NewHost(HostConfig{Policy: policy, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	runners := Runners{
		{Package: toolAlias, Name: "build"}: func(_ context.Context, execution *Execution) error {
			if err := execution.Exec("go"); err != nil {
				return err
			}
			return execution.Exec("go-alias")
		},
		{Package: endpointAlias, Name: "build"}: func(_ context.Context, execution *Execution) error {
			if err := execution.Connect("registry"); err != nil {
				return err
			}
			return execution.Connect("registry-alias")
		},
		{Package: envAlias, Name: "build"}: func(_ context.Context, execution *Execution) error {
			value, err := execution.Env("TOKEN")
			if err != nil {
				return err
			}
			if value != secret {
				t.Fatalf("authorized env value mismatch")
			}
			_, err = execution.Env("TOKEN_ALIAS")
			return err
		},
	}
	plan, err := host.Plan(graph)
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Run(context.Background(), plan, runners)
	if err != nil {
		t.Fatal(err)
	}
	findingFor(t, result, toolAlias, CodeUnregisteredTool)
	findingFor(t, result, endpointAlias, CodeUnknownEndpoint)
	findingFor(t, result, envAlias, CodeEnvironmentAbsent)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resultJSON), secret) {
		t.Fatal("result leaked an environment value")
	}
}

func TestChildCannotExpandBeyondInheritedCapabilities(t *testing.T) {
	networkChild := mustDigest(t, "pkg-network-child")
	narrowChild := mustDigest(t, "pkg-narrow-child")

	policy := NewPolicy()
	grant(t, policy, networkChild, Read("src/input"), Exec("go"), Connect("network"))
	grant(t, policy, narrowChild, Read("src/input"))

	catalog := NewCatalog()
	registerTool(t, catalog, "go")
	registerEndpoint(t, catalog, "network")
	files := NewVirtualFS()
	if err := files.AddSourceFile(networkChild, "src/input", []byte("network data")); err != nil {
		t.Fatal(err)
	}
	if err := files.AddSourceFile(narrowChild, "src/input", []byte("narrow data")); err != nil {
		t.Fatal(err)
	}

	graph := NewPackageGraph(
		PackageNode{Digest: networkChild, Scripts: []ScriptDeclaration{{
			Name: "build", Actions: []Action{Read("src/input"), Exec("go")},
		}}},
		PackageNode{Digest: narrowChild, Scripts: []ScriptDeclaration{{
			Name: "build", Actions: []Action{Read("src/input")},
		}}},
	)
	childRan := false
	runners := Runners{
		{Package: networkChild, Name: "build"}: func(ctx context.Context, execution *Execution) error {
			return execution.Spawn(ctx, "network-child", []Action{Connect("network")}, func(_ context.Context, child *Execution) error {
				childRan = true
				return child.Connect("network")
			})
		},
		{Package: narrowChild, Name: "build"}: func(ctx context.Context, execution *Execution) error {
			return execution.Spawn(ctx, "reader", []Action{Read("src/input")}, func(_ context.Context, child *Execution) error {
				_, err := child.Read("src/input")
				if err != nil {
					return err
				}
				return child.Write("out/read-copy", []byte("not declared"))
			})
		},
	}
	host, err := NewHost(HostConfig{Policy: policy, Catalog: catalog, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), runners)
	if err != nil {
		t.Fatal(err)
	}
	escalation := findingFor(t, result, networkChild, CodeSpawnEscalation)
	if escalation.Action != Connect("network") {
		t.Fatalf("wrong escalation action: %+v", escalation)
	}
	if len(escalation.Process) != 2 || escalation.Process[0] != "build" || escalation.Process[1] != "network-child" {
		t.Fatalf("wrong process evidence: %+v", escalation)
	}
	if childRan {
		t.Fatal("an overreaching child callback was invoked")
	}
	if result.Visible(networkChild) {
		t.Fatal("network child package became visible")
	}
	// The narrow child can read, but keeps the parent's declaration set: the
	// attempted write is undeclared and still fails the package.
	findingFor(t, result, narrowChild, CodeUndeclaredAction)
	if result.Visible(narrowChild) {
		t.Fatal("narrow child package with undeclared write became visible")
	}
}

func TestNarrowerChildInheritsAllowedSubset(t *testing.T) {
	pkg := mustDigest(t, "pkg-narrow-subset")
	policy := NewPolicy()
	grant(t, policy, pkg, Read("src/input"), Write("out/result"))
	files := NewVirtualFS()
	if err := files.AddSourceFile(pkg, "src/input", []byte("subset")); err != nil {
		t.Fatal(err)
	}
	graph := NewPackageGraph(PackageNode{Digest: pkg, Scripts: []ScriptDeclaration{{
		Name: "build", Actions: []Action{Read("src/input"), Write("out/result")},
	}}})
	host, err := NewHost(HostConfig{Policy: policy, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), Runners{
		{Package: pkg, Name: "build"}: func(ctx context.Context, execution *Execution) error {
			var copied []byte
			err := execution.Spawn(ctx, "reader", []Action{Read("src/input")}, func(_ context.Context, child *Execution) error {
				data, err := child.Read("src/input")
				if err != nil {
					return err
				}
				copied = data
				return nil
			})
			if err != nil {
				return err
			}
			return execution.Write("out/result", copied)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages[pkg].Status != StatusSucceeded {
		t.Fatalf("narrow child package failed: %+v", result.Violations)
	}
	if string(result.Published["out/result"].Data) != "subset" {
		t.Fatal("child output was not published")
	}
	var childEvent *Event
	for i := range result.Events {
		if len(result.Events[i].Process) == 2 && result.Events[i].Process[1] == "reader" {
			childEvent = &result.Events[i]
			break
		}
	}
	if childEvent == nil || !childEvent.Allowed || childEvent.Action != Read("src/input") {
		t.Fatalf("missing allowed child event: %+v", result.Events)
	}
}

func TestParallelPackagesConflictOnlyAtPublish(t *testing.T) {
	a := mustDigest(t, "pkg-a")
	b := mustDigest(t, "pkg-b")
	c := mustDigest(t, "pkg-c")
	d := mustDigest(t, "pkg-d")

	policy := NewPolicy()
	grant(t, policy, a, Write("bin/tool"))
	grant(t, policy, b, Write("bin/tool"))
	grant(t, policy, c, Write("bin/c"))
	grant(t, policy, d, Write("bin/d"))

	graph := NewPackageGraph(
		PackageNode{Digest: a, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/tool")}}}},
		PackageNode{Digest: b, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/tool")}}}},
		PackageNode{Digest: c, DependsOn: []Digest{b}, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/c")}}}},
		PackageNode{Digest: d, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/d")}}}},
	)
	host, err := NewHost(HostConfig{Policy: policy, MaxParallel: 3})
	if err != nil {
		t.Fatal(err)
	}
	runners := Runners{
		{Package: a, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("bin/tool", []byte("from-a"))
		},
		{Package: b, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("bin/tool", []byte("from-b"))
		},
		{Package: c, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("bin/c", []byte("from-c"))
		},
		{Package: d, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("bin/d", []byte("from-d"))
		},
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), runners)
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages[a].Status != StatusSucceeded || result.Packages[b].Status != StatusConflict {
		t.Fatalf("unexpected conflict statuses: a=%s b=%s", result.Packages[a].Status, result.Packages[b].Status)
	}
	if result.Packages[c].Status != StatusRevoked {
		t.Fatalf("dependent of conflicted package was not revoked: %s", result.Packages[c].Status)
	}
	if result.Packages[d].Status != StatusSucceeded {
		t.Fatalf("unrelated package was not preserved: %s", result.Packages[d].Status)
	}
	if string(result.Published["bin/tool"].Data) != "from-a" {
		t.Fatalf("wrong winning output: %+v", result.Published["bin/tool"])
	}
	if _, exists := result.Published["bin/c"]; exists {
		t.Fatal("revoked dependent output became visible")
	}
	if string(result.Published["bin/d"].Data) != "from-d" {
		t.Fatal("unrelated output was not published")
	}
	conflict := findingFor(t, result, b, CodePublishConflict)
	if conflict.Action != Write("bin/tool") || conflict.Script != "build" {
		t.Fatalf("missing package-to-action conflict evidence: %+v", conflict)
	}
	revocation := revocationFor(t, result, c)
	if revocation.Cause != b || len(revocation.Chain) != 2 || revocation.Chain[0] != b || revocation.Chain[1] != c {
		t.Fatalf("bad dependency evidence: %+v", revocation)
	}
	if !host.Cache().Has(a) || !host.Cache().Has(d) || host.Cache().Has(b) || host.Cache().Has(c) {
		t.Fatalf("unexpected cache after conflict: %v", host.Cache().Digests())
	}
	if len(host.Files().OutputFiles(b)) != 0 || len(host.Files().OutputFiles(c)) != 0 {
		t.Fatal("conflicted or revoked staging output remained readable")
	}
}

func TestCallbackFailureRollsBackPackageAndLeavesUnrelatedCache(t *testing.T) {
	a := mustDigest(t, "pkg-success-a")
	b := mustDigest(t, "pkg-failing-b")
	c := mustDigest(t, "pkg-dependent-c")
	d := mustDigest(t, "pkg-success-d")

	policy := NewPolicy()
	grant(t, policy, a, Write("out/a"))
	grant(t, policy, b, Write("out/b"))
	grant(t, policy, c, Write("out/c"))
	grant(t, policy, d, Write("out/d"))

	graph := NewPackageGraph(
		PackageNode{Digest: a, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("out/a")}}}},
		PackageNode{Digest: b, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("out/b")}}}},
		PackageNode{Digest: c, DependsOn: []Digest{b}, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("out/c")}}}},
		PackageNode{Digest: d, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("out/d")}}}},
	)
	files := NewVirtualFS()
	host, err := NewHost(HostConfig{Policy: policy, Files: files, MaxParallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	runners := Runners{
		{Package: a, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("out/a", []byte("a"))
		},
		{Package: b, Name: "build"}: func(_ context.Context, execution *Execution) error {
			if err := execution.Write("out/b", []byte("partial-b")); err != nil {
				return err
			}
			return errors.New("synthetic build failure")
		},
		{Package: c, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("out/c", []byte("c"))
		},
		{Package: d, Name: "build"}: func(_ context.Context, execution *Execution) error {
			return execution.Write("out/d", []byte("d"))
		},
	}
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), runners)
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages[a].Status != StatusSucceeded || result.Packages[d].Status != StatusSucceeded {
		t.Fatal("unrelated packages did not complete")
	}
	if result.Packages[b].Status != StatusFailed || result.Packages[c].Status != StatusRevoked {
		t.Fatalf("failure did not revoke successors: b=%s c=%s", result.Packages[b].Status, result.Packages[c].Status)
	}
	if _, exists := result.Published["out/b"]; exists {
		t.Fatal("failed package output became visible")
	}
	if _, exists := result.Published["out/c"]; exists {
		t.Fatal("revoked dependent output became visible")
	}
	if len(files.OutputFiles(b)) != 0 {
		t.Fatal("failed package staging root was not discarded")
	}
	findingFor(t, result, b, CodeCallbackFailure)
	revocation := revocationFor(t, result, c)
	if revocation.Cause != b || len(revocation.Chain) != 2 {
		t.Fatalf("bad failure dependency evidence: %+v", revocation)
	}
	if !host.Cache().Has(a) || !host.Cache().Has(d) || host.Cache().Has(b) || host.Cache().Has(c) {
		t.Fatalf("unexpected cache after failure: %v", host.Cache().Digests())
	}

	entry, ok := host.Cache().Get(a)
	if !ok {
		t.Fatal("missing unrelated cache entry")
	}
	entry.Files["out/a"][0] = 'X'
	again, ok := host.Cache().Get(a)
	if !ok || again.Files["out/a"][0] != 'a' {
		t.Fatal("cache entry was mutable")
	}
}

func TestCachedPackageSkipsCallbackAndKeepsImmutableOutput(t *testing.T) {
	pkg := mustDigest(t, "pkg-cached")
	policy := NewPolicy()
	grant(t, policy, pkg, Write("out/file"))
	graph := NewPackageGraph(PackageNode{Digest: pkg, Scripts: []ScriptDeclaration{{
		Name: "build", Actions: []Action{Write("out/file")},
	}}})
	host, err := NewHost(HostConfig{Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := func(_ context.Context, execution *Execution) error {
		calls++
		return execution.Write("out/file", []byte("cached"))
	}
	plan, err := host.Plan(graph)
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.Run(context.Background(), plan, Runners{{Package: pkg, Name: "build"}: runner})
	if err != nil {
		t.Fatal(err)
	}
	if first.Packages[pkg].Status != StatusSucceeded {
		t.Fatal("first run did not succeed")
	}
	second, err := host.Run(context.Background(), plan, Runners{{Package: pkg, Name: "build"}: runner})
	if err != nil {
		t.Fatal(err)
	}
	if second.Packages[pkg].Status != StatusCached || !second.Packages[pkg].Cached {
		t.Fatal("second run did not use cache")
	}
	if calls != 1 {
		t.Fatalf("cached callback ran again: %d", calls)
	}
	if string(second.Published["out/file"].Data) != "cached" {
		t.Fatal("cached output was not published")
	}
}

func TestModifiedPlanCannotExpandCapabilities(t *testing.T) {
	pkg := mustDigest(t, "pkg-plan-integrity")
	policy := NewPolicy()
	grant(t, policy, pkg, Write("out/file"))
	catalog := NewCatalog()
	registerTool(t, catalog, "go")
	graph := NewPackageGraph(PackageNode{Digest: pkg, Scripts: []ScriptDeclaration{{
		Name: "build", Actions: []Action{Write("out/file")},
	}}})
	host, err := NewHost(HostConfig{Policy: policy, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, host, graph)
	manifest := plan.Manifests[pkg]
	manifest.Scripts[0].Declared = append(manifest.Scripts[0].Declared, Exec("go"))
	manifest.Scripts[0].Granted = append(manifest.Scripts[0].Granted, Exec("go"))
	plan.Manifests[pkg] = manifest
	called := false
	_, err = host.Run(context.Background(), plan, Runners{
		{Package: pkg, Name: "build"}: func(_ context.Context, execution *Execution) error {
			called = true
			return execution.Exec("go")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("modified plan was accepted: %v", err)
	}
	if called {
		t.Fatal("callback ran for a modified plan")
	}
}

func TestSourceRootsAreSealedDuringCallbacks(t *testing.T) {
	pkg := mustDigest(t, "pkg-sealed-source")
	policy := NewPolicy()
	grant(t, policy, pkg, Read("src/input"), Write("out/copy"))
	files := NewVirtualFS()
	if err := files.AddSourceFile(pkg, "src/input", []byte("original")); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(HostConfig{Policy: policy, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	graph := NewPackageGraph(PackageNode{Digest: pkg, Scripts: []ScriptDeclaration{{
		Name: "build", Actions: []Action{Read("src/input"), Write("out/copy")},
	}}})
	result, err := host.Run(context.Background(), mustPlan(t, host, graph), Runners{
		{Package: pkg, Name: "build"}: func(_ context.Context, execution *Execution) error {
			if err := files.AddSourceFile(pkg, "src/injected", []byte("changed")); err == nil {
				return errors.New("source root accepted a mutation during execution")
			}
			if err := files.ResetOutput(pkg); err == nil {
				return errors.New("output root accepted a public reset during execution")
			}
			data, err := execution.Read("src/input")
			if err != nil {
				return err
			}
			return execution.Write("out/copy", data)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages[pkg].Status != StatusSucceeded {
		t.Fatalf("sealed-root callback failed: %+v", result.Violations)
	}
	if _, exists := files.SourceFiles(pkg)["src/injected"]; exists {
		t.Fatal("source root was mutated during execution")
	}
}

func TestPublishRejectsFileDirectoryCollision(t *testing.T) {
	t.Run("published parent then child", func(t *testing.T) {
		file := mustDigest(t, "pkg-a-file")
		child := mustDigest(t, "pkg-b-child")
		policy := NewPolicy()
		grant(t, policy, file, Write("bin"))
		grant(t, policy, child, Write("bin/tool"))
		graph := NewPackageGraph(
			PackageNode{Digest: file, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin")}}}},
			PackageNode{Digest: child, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/tool")}}}},
		)
		host, err := NewHost(HostConfig{Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		result, err := host.Run(context.Background(), mustPlan(t, host, graph), Runners{
			{Package: file, Name: "build"}: func(_ context.Context, execution *Execution) error {
				return execution.Write("bin", []byte("file"))
			},
			{Package: child, Name: "build"}: func(_ context.Context, execution *Execution) error {
				return execution.Write("bin/tool", []byte("child"))
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Packages[file].Status != StatusSucceeded || result.Packages[child].Status != StatusConflict {
			t.Fatalf("collision was not rejected: file=%s child=%s", result.Packages[file].Status, result.Packages[child].Status)
		}
		if _, exists := result.Published["bin/tool"]; exists {
			t.Fatal("child path was published below a package-owned file")
		}
		findingFor(t, result, child, CodePublishConflict)
	})

	t.Run("published child then parent", func(t *testing.T) {
		child := mustDigest(t, "pkg-a-child")
		file := mustDigest(t, "pkg-b-file")
		policy := NewPolicy()
		grant(t, policy, child, Write("bin/tool"))
		grant(t, policy, file, Write("bin"))
		graph := NewPackageGraph(
			PackageNode{Digest: child, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin/tool")}}}},
			PackageNode{Digest: file, Scripts: []ScriptDeclaration{{Name: "build", Actions: []Action{Write("bin")}}}},
		)
		host, err := NewHost(HostConfig{Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		result, err := host.Run(context.Background(), mustPlan(t, host, graph), Runners{
			{Package: child, Name: "build"}: func(_ context.Context, execution *Execution) error {
				return execution.Write("bin/tool", []byte("child"))
			},
			{Package: file, Name: "build"}: func(_ context.Context, execution *Execution) error {
				return execution.Write("bin", []byte("file"))
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Packages[child].Status != StatusSucceeded || result.Packages[file].Status != StatusConflict {
			t.Fatalf("collision was not rejected: child=%s file=%s", result.Packages[child].Status, result.Packages[file].Status)
		}
		if _, exists := result.Published["bin"]; exists {
			t.Fatal("package file replaced an already published subtree")
		}
		findingFor(t, result, file, CodePublishConflict)
	})
}

func revocationFor(t *testing.T, result *Result, digest Digest) Revocation {
	t.Helper()
	for _, revocation := range result.Revocations {
		if revocation.Package == digest {
			return revocation
		}
	}
	t.Fatalf("no revocation evidence for %s; got %+v", digest, result.Revocations)
	return Revocation{}
}

func mustPlan(t *testing.T, host *Host, graph PackageGraph) *Plan {
	t.Helper()
	plan, err := host.Plan(graph)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
