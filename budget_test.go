package netsec

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNestedDoublingAliasesRejected(t *testing.T) {
	data := nestedDoublingYAML(40)
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	stats, err := Analyze(root, Limits{MaxNodes: 1000, MaxDepth: 1000, MaxMergeDepth: 100})
	if err == nil {
		t.Fatalf("expected doubling aliases to be rejected, stats=%v", stats)
	}
	if !errors.Is(err, ErrNodesExceeded) {
		t.Fatalf("expected ErrNodesExceeded, got %v", err)
	}
	if stats.Nodes <= 1000 {
		t.Fatalf("estimate did not grow: %+v", stats)
	}
	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *LimitError, got %T", err)
	}
	if limitErr.Line <= 1 {
		t.Fatalf("limit error did not retain a source line: %+v", limitErr)
	}
}

func TestNestedBlockAliasesRejected(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("a0: &a0\n  - x\n  - x\n")
	for i := 1; i < 25; i++ {
		fmt.Fprintf(&builder, "a%d: &a%d\n  - *a%d\n  - *a%d\n", i, i, i-1, i-1)
	}
	fmt.Fprintf(&builder, "root: *a24\n")
	root, err := Parse([]byte(builder.String()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 1000, MaxDepth: 1000, MaxMergeDepth: 100})
	if !errors.Is(err, ErrNodesExceeded) {
		t.Fatalf("expected block alias rejection, got %v (stats=%v)", err, stats)
	}
}

func TestNestedDoublingAliasesSaturateWithoutOverflow(t *testing.T) {
	data := nestedDoublingYAML(80)
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 1000, MaxDepth: 1000, MaxMergeDepth: 100})
	if !errors.Is(err, ErrNodesExceeded) {
		t.Fatalf("expected saturation to be rejected, got %v", err)
	}
	if stats.Nodes <= 0 {
		t.Fatalf("node estimate overflowed: %+v", stats)
	}
	if stats.Nodes != math.MaxInt {
		t.Fatalf("expected saturated node count, got %d", stats.Nodes)
	}
}

func TestMergeKeyArraySemanticsAndLimits(t *testing.T) {
	data := `
defaults: &defaults
  image: alpine
  retries: 2
extra: &extra
  timeout: 10
  retries: 9
job:
  <<: [*defaults, *extra]
  retries: 3
reverse:
  <<: [*extra, *defaults]
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	stats, err := Analyze(root, Limits{MaxNodes: 2000, MaxDepth: 20, MaxMergeDepth: 3})
	if err != nil {
		t.Fatalf("valid merge array rejected: %v (stats=%v)", err, stats)
	}
	if stats.MergeDepth != 1 {
		t.Fatalf("merge depth = %d, want 1", stats.MergeDepth)
	}

	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	job := decoded["job"].(map[string]interface{})
	if job["image"] != "alpine" || job["timeout"] != 10 || job["retries"] != 3 {
		t.Fatalf("merge semantics lost: %#v", job)
	}
	reverse := decoded["reverse"].(map[string]interface{})
	if reverse["retries"] != 9 {
		t.Fatalf("first merge target should win: %#v", reverse)
	}

	assertLinePreserved(t, root, 3)
}

func TestMultilineMergeKeyArray(t *testing.T) {
	data := `
defaults: &defaults {image: app, retries: 2}
extra: &extra {timeout: 30}
job:
  <<: [
    *defaults,
    *extra
  ]
  retries: 3
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Check(root, Limits{MaxNodes: 2000, MaxDepth: 20, MaxMergeDepth: 3}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	job := decoded["job"].(map[string]interface{})
	if job["image"] != "app" || job["timeout"] != 30 || job["retries"] != 3 {
		t.Fatalf("multiline merge = %#v", job)
	}
}

func TestBlockSequenceMergeKeyArray(t *testing.T) {
	data := `
defaults: &defaults {image: app}
extra: &extra {timeout: 30}
job:
  <<:
    - *defaults
    - *extra
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 2000, MaxDepth: 20, MaxMergeDepth: 3})
	if err != nil {
		t.Fatalf("Analyze: %v (stats=%v)", err, stats)
	}
	if stats.MergeDepth != 1 {
		t.Fatalf("merge depth = %d, want 1", stats.MergeDepth)
	}
	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	job := decoded["job"].(map[string]interface{})
	if job["image"] != "app" || job["timeout"] != 30 {
		t.Fatalf("block merge = %#v", job)
	}
}

func TestMergeKeyArrayRejectedByNodeBudget(t *testing.T) {
	data := `
a: &a {x: 1}
b: &b {y: 2}
c: &c {z: 3}
root:
  <<: [*a, *b, *c]
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Analyze(root, Limits{MaxNodes: 4, MaxDepth: 100, MaxMergeDepth: 100})
	if !errors.Is(err, ErrNodesExceeded) {
		t.Fatalf("expected node rejection, got %v", err)
	}
}

func TestAliasCycleRejected(t *testing.T) {
	anchor := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Anchor: "loop", Line: 3, Column: 1}
	alias := &yaml.Node{Kind: yaml.AliasNode, Value: "loop", Alias: anchor, Line: 3, Column: 5}
	anchor.Content = []*yaml.Node{alias}
	root := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Line: 1,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "cycle", Line: 3, Column: 1},
			anchor,
		},
	}

	_, err := Analyze(root, Limits{MaxNodes: 1000, MaxDepth: 100, MaxMergeDepth: 10})
	if !errors.Is(err, ErrAliasCycle) {
		t.Fatalf("expected alias cycle rejection, got %v", err)
	}
	var cycle *CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("expected *CycleError, got %T", err)
	}
	if cycle.Line != 3 {
		t.Fatalf("cycle line = %d, want 3", cycle.Line)
	}
}

func TestParsedAliasCycleRejected(t *testing.T) {
	root, err := Parse([]byte("cycle: &loop [*loop]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Check(root, Limits{MaxNodes: 100, MaxDepth: 10, MaxMergeDepth: 10}); !errors.Is(err, ErrAliasCycle) {
		t.Fatalf("expected alias cycle rejection, got %v", err)
	}
}

func TestParsedBlockAliasCycleRejected(t *testing.T) {
	for _, data := range []string{
		"a: &a\n  b: *a\n",
		"a: &a\n  <<: *a\n",
		"a: &a\n  - *a\n",
	} {
		root, err := Parse([]byte(data))
		if err != nil {
			t.Fatalf("Parse %q: %v", data, err)
		}
		if err := Check(root, Limits{MaxNodes: 100, MaxDepth: 10, MaxMergeDepth: 10}); !errors.Is(err, ErrAliasCycle) {
			t.Fatalf("data %q: expected alias cycle rejection, got %v", data, err)
		}
	}
}

func TestInvalidAliasRejected(t *testing.T) {
	root := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Line: 1,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x", Line: 1},
			{Kind: yaml.AliasNode, Value: "missing", Line: 2},
		},
	}
	err := Check(root, Limits{MaxNodes: 100, MaxDepth: 10, MaxMergeDepth: 10})
	if !errors.Is(err, ErrInvalidAlias) {
		t.Fatalf("expected invalid alias rejection, got %v", err)
	}
}

func TestNormalSharedConfigPassesAndKeepsLines(t *testing.T) {
	data := `
base: &base
  image: app:1.2
  env: [LOG_LEVEL, REGION]
prod:
  <<: *base
  replicas: 3
stage:
  <<: *base
  replicas: 1
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 1000, MaxDepth: 20, MaxMergeDepth: 4})
	if err != nil {
		t.Fatalf("normal shared config rejected: %v (stats=%v)", err, stats)
	}
	if stats.Nodes <= 0 || stats.Depth <= 0 {
		t.Fatalf("bad stats: %+v", stats)
	}
	assertLinePreserved(t, root, 3)

	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	prod := decoded["prod"].(map[string]interface{})
	if prod["image"] != "app:1.2" || prod["replicas"] != 3 {
		t.Fatalf("shared config not materialized: %#v", prod)
	}
	env := prod["env"].([]interface{})
	if len(env) != 2 || env[0] != "LOG_LEVEL" {
		t.Fatalf("shared sequence not materialized: %#v", env)
	}
}

func TestDepthAndMergeDepthLimits(t *testing.T) {
	depthData := nestedAliasDepthYAML(12)
	root, err := Parse([]byte(depthData))
	if err != nil {
		t.Fatalf("Parse depth data: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 10000, MaxDepth: 8, MaxMergeDepth: 100})
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("expected depth rejection, got %v (stats=%v)", err, stats)
	}
	if stats.Depth <= 8 {
		t.Fatalf("depth estimate did not grow: %+v", stats)
	}

	mergeData := `
a: &a {base: 1}
b: &b {<<: *a, middle: 2}
c: &c {<<: *b, top: 3}
root: *c
`
	mergeRoot, err := Parse([]byte(mergeData))
	if err != nil {
		t.Fatalf("Parse merge data: %v", err)
	}
	stats, err = Analyze(mergeRoot, Limits{MaxNodes: 10000, MaxDepth: 100, MaxMergeDepth: 1})
	if !errors.Is(err, ErrMergeDepthExceeded) {
		t.Fatalf("expected merge-depth rejection, got %v (stats=%v)", err, stats)
	}
	if stats.MergeDepth != 2 {
		t.Fatalf("merge depth = %d, want 2", stats.MergeDepth)
	}
	if _, err := Analyze(mergeRoot, Limits{MaxNodes: 10000, MaxDepth: 100, MaxMergeDepth: 2}); err != nil {
		t.Fatalf("merge depth 2 should pass: %v", err)
	}
}

func TestQuotedMergeKeyIsOrdinaryKey(t *testing.T) {
	root, err := Parse([]byte("m:\n  \"<<\": 1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stats, err := Analyze(root, Limits{MaxNodes: 100, MaxDepth: 10, MaxMergeDepth: 1})
	if err != nil {
		t.Fatalf("quoted merge-looking key rejected: %v", err)
	}
	if stats.MergeDepth != 0 {
		t.Fatalf("quoted \"<<\" counted as merge: %+v", stats)
	}
}

func TestFlowQuotedMergeArrayAndSequenceMapping(t *testing.T) {
	data := `base: &base {image: "app:1.2", retries: 2}
other: &other {timeout: 30}
jobs:
  - name: build
    <<: [*base, *other]
  - name: test
    <<: *base
    retries: 1
flow: {<<: [*base, *other], note: "ok"}
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Check(root, Limits{MaxNodes: 5000, MaxDepth: 50, MaxMergeDepth: 4}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	jobs := decoded["jobs"].([]interface{})
	build := jobs[0].(map[string]interface{})
	if build["image"] != "app:1.2" || build["retries"] != 2 || build["timeout"] != 30 {
		t.Fatalf("block sequence merge = %#v", build)
	}
	test := jobs[1].(map[string]interface{})
	if test["retries"] != 1 || test["image"] != "app:1.2" {
		t.Fatalf("sequence merge override = %#v", test)
	}
	flow := decoded["flow"].(map[string]interface{})
	if flow["image"] != "app:1.2" || flow["timeout"] != 30 || flow["note"] != "ok" {
		t.Fatalf("flow merge = %#v", flow)
	}
}

func TestMultiDocumentDecoder(t *testing.T) {
	data := "a: &a {x: 1}\n---\nbase: &a {x: 1}\nb:\n  <<: *a\n"
	decoder := yaml.NewDecoder(strings.NewReader(data))
	var first, second map[string]interface{}
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("first Decode: %v", err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatalf("second Decode: %v", err)
	}
	if first["a"] == nil {
		t.Fatalf("first document = %#v", first)
	}
	b := second["b"].(map[string]interface{})
	if b["x"] != 1 {
		t.Fatalf("second document merge = %#v", b)
	}
}

func TestBlockScalarDoesNotConfuseFlowJoining(t *testing.T) {
	data := `script: |
  echo [
  echo ]
base: &base {image: app}
job:
  <<: *base
`
	root, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Check(root, Limits{MaxNodes: 1000, MaxDepth: 20, MaxMergeDepth: 3}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	var decoded map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded["script"] != "echo [\necho ]\n" {
		t.Fatalf("block scalar = %q", decoded["script"])
	}
	job := decoded["job"].(map[string]interface{})
	if job["image"] != "app" {
		t.Fatalf("merge after block scalar = %#v", job)
	}
}

func TestLoaderNotCalledForRejectedDocuments(t *testing.T) {
	cases := []struct {
		name string
		data string
		lim  Limits
	}{
		{
			name: "doubling aliases",
			data: nestedDoublingYAML(30),
			lim:  Limits{MaxNodes: 100, MaxDepth: 100, MaxMergeDepth: 100},
		},
		{
			name: "alias cycle",
			data: "cycle: &loop [*loop]\n",
			lim:  Limits{MaxNodes: 100, MaxDepth: 100, MaxMergeDepth: 100},
		},
		{
			name: "merge key array",
			data: "a: &a {x: 1}\nb: &b {y: 2}\nroot:\n  <<: [*a, *b]\n",
			lim:  Limits{MaxNodes: 3, MaxDepth: 100, MaxMergeDepth: 100},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var out map[string]interface{}
			load := func([]byte, interface{}) error {
				calls++
				return nil
			}
			err := DecodeWith([]byte(tc.data), &out, load, tc.lim)
			if err == nil {
				t.Fatalf("expected rejection")
			}
			if calls != 0 {
				t.Fatalf("loader called %d times for rejected input", calls)
			}
		})
	}
}

func TestLoaderCalledForValidSharedConfig(t *testing.T) {
	data := "base: &base {image: app, retries: 2}\nprod:\n  <<: *base\n  replicas: 3\n"
	calls := 0
	var decoded map[string]interface{}
	load := func(data []byte, out interface{}) error {
		calls++
		return yaml.Unmarshal(data, out)
	}
	err := DecodeWith([]byte(data), &decoded, load, Limits{MaxNodes: 1000, MaxDepth: 20, MaxMergeDepth: 4})
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
	prod := decoded["prod"].(map[string]interface{})
	if prod["image"] != "app" || prod["retries"] != 2 || prod["replicas"] != 3 {
		t.Fatalf("merged config = %#v", prod)
	}
}

func TestLoadAcceptsLimitsAndLoaderInEitherOrder(t *testing.T) {
	data := "base: &base {image: app}\njob:\n  <<: *base\n"
	limits := Limits{MaxNodes: 1000, MaxDepth: 20, MaxMergeDepth: 3}
	calls := 0
	loader := LoadFunc(func(data []byte, out interface{}) error {
		calls++
		return yaml.Unmarshal(data, out)
	})

	var first, second map[string]interface{}
	if err := Load([]byte(data), &first, limits, loader); err != nil {
		t.Fatalf("Load limits,loader: %v", err)
	}
	if err := Load([]byte(data), &second, loader, limits); err != nil {
		t.Fatalf("Load loader,limits: %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2", calls)
	}
	for _, decoded := range []map[string]interface{}{first, second} {
		job := decoded["job"].(map[string]interface{})
		if job["image"] != "app" {
			t.Fatalf("job = %#v", job)
		}
	}
}

func TestBudgetReusableAndPreflight(t *testing.T) {
	budget := NewBudget(Limits{MaxNodes: 1000, MaxDepth: 20, MaxMergeDepth: 4})
	valid := "a: &a {x: 1}\nb:\n  <<: *a\n"
	root, stats, err := Preflight([]byte(valid), budget.Limits)
	if err != nil {
		t.Fatalf("Preflight valid: %v (stats=%v)", err, stats)
	}
	if root == nil || stats.Nodes == 0 {
		t.Fatalf("Preflight returned no tree/stats")
	}
	if err := budget.Check(root); err != nil {
		t.Fatalf("Budget.Check valid: %v", err)
	}
}

func nestedDoublingYAML(levels int) string {
	var builder strings.Builder
	builder.WriteString("a0: &a0 [x, x]\n")
	for i := 1; i < levels; i++ {
		fmt.Fprintf(&builder, "a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1)
	}
	fmt.Fprintf(&builder, "root: *a%d\n", levels-1)
	return builder.String()
}

func nestedAliasDepthYAML(levels int) string {
	var builder strings.Builder
	builder.WriteString("n0: &n0 [leaf]\n")
	for i := 1; i < levels; i++ {
		fmt.Fprintf(&builder, "n%d: &n%d [*n%d]\n", i, i, i-1)
	}
	fmt.Fprintf(&builder, "root: *n%d\n", levels-1)
	return builder.String()
}

func assertLinePreserved(t *testing.T, root *yaml.Node, wantLine int) {
	t.Helper()
	doc := root
	if doc.Kind == yaml.DocumentNode {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode || len(doc.Content) < 4 {
		t.Fatalf("unexpected root shape: %+v", doc)
	}
	anchorValue := doc.Content[1]
	if anchorValue.Anchor == "" {
		t.Fatalf("second mapping value is not an anchor: %+v", anchorValue)
	}
	if anchorValue.Line != wantLine {
		t.Fatalf("anchor line = %d, want %d", anchorValue.Line, wantLine)
	}
	if err := Check(root, Limits{MaxNodes: 5000, MaxDepth: 50, MaxMergeDepth: 5}); err != nil {
		t.Fatalf("Check after line assertion: %v", err)
	}
	if anchorValue.Line != wantLine {
		t.Fatalf("Check mutated anchor line: %d", anchorValue.Line)
	}
}
