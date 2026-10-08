package snapaudit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// jsonValue renders a violation value compactly so expectations can
// be written as JSON literals; nil renders as "null".
func jsonValue(v interface{}) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type wantViolation struct {
	path   string
	reason Reason
	old    string // JSON literal, "null" when absent
	new    string // JSON literal, "null" when absent
}

// TestCompare expands every rule leaf by leaf: each case builds the
// two snapshots from JSON, applies the policy, and checks the exact
// set of violations including the old/new values at each path.
func TestCompare(t *testing.T) {
	cases := []struct {
		name   string
		old    string
		new    string
		policy Policy
		want   []wantViolation
	}{
		{
			name: "identical snapshots",
			old:  `{"spec":{"replicas":3}}`,
			new:  `{"spec":{"replicas":3}}`,
			policy: Policy{
				Immutable: []string{"spec.replicas"},
			},
			want: nil,
		},
		{
			name: "scalar modification allowed by default",
			old:  `{"spec":{"replicas":3}}`,
			new:  `{"spec":{"replicas":5}}`,
			want: nil,
		},
		{
			name: "immutable leaf modified",
			old:  `{"spec":{"replicas":3}}`,
			new:  `{"spec":{"replicas":5}}`,
			policy: Policy{
				Immutable: []string{"spec.replicas"},
			},
			want: []wantViolation{
				{"spec.replicas", ReasonModified, "3", "5"},
			},
		},
		{
			name: "immutable parent reports shallowest path only",
			old:  `{"spec":{"replicas":3,"paused":false}}`,
			new:  `{"spec":{"replicas":5,"paused":true}}`,
			policy: Policy{
				Immutable: []string{"spec"},
			},
			want: []wantViolation{
				{"spec", ReasonModified, `{"paused":false,"replicas":3}`, `{"paused":true,"replicas":5}`},
			},
		},
		{
			name: "parent replacement still checks protected descendants",
			old:  `{"spec":{"replicas":3,"paused":false}}`,
			new:  `{"spec":{"paused":true}}`,
			policy: Policy{
				Immutable:   []string{"spec.replicas"},
				AllowDelete: []string{"spec.paused"},
			},
			want: []wantViolation{
				{"spec.replicas", ReasonRemoved, "3", "null"},
			},
		},
		{
			name: "wildcard matches exactly one level",
			old:  `{"spec":{"containers":{"app":{"image":"v1"}}}}`,
			new:  `{"spec":{"containers":{"app":{"image":"v2"}}}}`,
			policy: Policy{
				Immutable: []string{"spec.containers.*.image"},
			},
			want: []wantViolation{
				{"spec.containers.app.image", ReasonModified, `"v1"`, `"v2"`},
			},
		},
		{
			name: "wildcard does not match deeper levels",
			old:  `{"spec":{"containers":{"app":{"meta":{"image":"v1"}}}}}`,
			new:  `{"spec":{"containers":{"app":{"meta":{"image":"v2"}}}}}`,
			policy: Policy{
				Immutable: []string{"spec.containers.*.image"},
			},
			want: nil,
		},
		{
			name: "wildcard does not match the parent itself",
			old:  `{"spec":{"containers":{"app":"v1"}}}`,
			new:  `{"spec":{"containers":{"app":"v2"}}}`,
			policy: Policy{
				Immutable: []string{"spec.containers.*.image"},
			},
			want: nil,
		},
		{
			name: "addition expands per leaf without allow rule",
			old:  `{"metadata":{"name":"web"}}`,
			new:  `{"metadata":{"name":"web","labels":{"team":"a","env":"prod"}}}`,
			want: []wantViolation{
				{"metadata.labels.env", ReasonAdded, "null", `"prod"`},
				{"metadata.labels.team", ReasonAdded, "null", `"a"`},
			},
		},
		{
			name: "addition allowed at subtree root",
			old:  `{"metadata":{"name":"web"}}`,
			new:  `{"metadata":{"name":"web","labels":{"team":"a"}}}`,
			policy: Policy{
				AllowAdd: []string{"metadata.labels"},
			},
			want: nil,
		},
		{
			name: "addition allowed per leaf by wildcard",
			old:  `{"metadata":{"name":"web"}}`,
			new:  `{"metadata":{"name":"web","labels":{"team":"a","env":"prod"}}}`,
			policy: Policy{
				AllowAdd: []string{"metadata.labels.*"},
			},
			want: nil,
		},
		{
			name: "add allow wildcard does not match the parent itself",
			old:  `{"metadata":{"name":"web"}}`,
			new:  `{"metadata":{"name":"web","labels":"oops"}}`,
			policy: Policy{
				AllowAdd: []string{"metadata.labels.*"},
			},
			want: []wantViolation{
				{"metadata.labels", ReasonAdded, "null", `"oops"`},
			},
		},
		{
			name: "removal requires allow rule",
			old:  `{"spec":{"replicas":3,"paused":false}}`,
			new:  `{"spec":{"replicas":3}}`,
			want: []wantViolation{
				{"spec.paused", ReasonRemoved, "false", "null"},
			},
		},
		{
			name: "removal allowed",
			old:  `{"spec":{"replicas":3,"paused":false}}`,
			new:  `{"spec":{"replicas":3}}`,
			policy: Policy{
				AllowDelete: []string{"spec.paused"},
			},
			want: nil,
		},
		{
			name: "removed subtree exposes protected descendant leaf",
			old:  `{"spec":{"template":{"spec":{"replicas":3}}}}`,
			new:  `{"spec":{}}`,
			policy: Policy{
				Immutable: []string{"spec.template.spec.replicas"},
			},
			want: []wantViolation{
				{"spec.template.spec.replicas", ReasonRemoved, "3", "null"},
			},
		},
		{
			name: "removed subtree allowed at its root",
			old:  `{"spec":{"template":{"spec":{"replicas":3}}}}`,
			new:  `{"spec":{}}`,
			policy: Policy{
				AllowDelete: []string{"spec.template"},
			},
			want: nil,
		},
		{
			name: "array element matched by index wildcard",
			old:  `{"items":[{"id":1},{"id":2}]}`,
			new:  `{"items":[{"id":1},{"id":9}]}`,
			policy: Policy{
				Immutable: []string{"items.*.id"},
			},
			want: []wantViolation{
				{"items.1.id", ReasonModified, "2", "9"},
			},
		},
		{
			name: "array append expands per leaf without allow rule",
			old:  `{"items":[{"id":1}]}`,
			new:  `{"items":[{"id":1},{"id":2}]}`,
			want: []wantViolation{
				{"items.1.id", ReasonAdded, "null", "2"},
			},
		},
		{
			name: "array append allowed by index wildcard",
			old:  `{"items":[{"id":1}]}`,
			new:  `{"items":[{"id":1},{"id":2}]}`,
			policy: Policy{
				AllowAdd: []string{"items.*"},
			},
			want: nil,
		},
		{
			name: "kind change object to scalar checks protected leaves",
			old:  `{"spec":{"replicas":3}}`,
			new:  `{"spec":7}`,
			policy: Policy{
				Immutable: []string{"spec.replicas"},
			},
			want: []wantViolation{
				{"spec.replicas", ReasonRemoved, "3", "null"},
			},
		},
		{
			name: "kind change scalar to object checks added leaves",
			old:  `{"spec":7}`,
			new:  `{"spec":{"replicas":3}}`,
			want: []wantViolation{
				{"spec.replicas", ReasonAdded, "null", "3"},
			},
		},
		{
			name: "violations ordered shallowest first",
			old:  `{"a":{"x":{"y":1}},"b":{"c":1}}`,
			new:  `{"a":{"x":{"y":2}},"b":{"c":2}}`,
			policy: Policy{
				Immutable: []string{"a.x.y", "b"},
			},
			want: []wantViolation{
				{"b", ReasonModified, `{"c":1}`, `{"c":2}`},
				{"a.x.y", ReasonModified, "1", "2"},
			},
		},
	}

	for _, tc := range cases {
		violations, err := CompareJSON([]byte(tc.old), []byte(tc.new), tc.policy)
		if err != nil {
			t.Errorf("%s: CompareJSON error: %v", tc.name, err)
			continue
		}
		if len(violations) != len(tc.want) {
			t.Errorf("%s: got %d violations %+v, want %d", tc.name, len(violations), violations, len(tc.want))
			continue
		}
		for i, want := range tc.want {
			got := violations[i]
			if got.Path != want.path || got.Reason != want.reason {
				t.Errorf("%s: violation %d = %s (%s), want %s (%s)",
					tc.name, i, got.Path, got.Reason, want.path, want.reason)
				continue
			}
			if jsonValue(got.Old) != want.old || jsonValue(got.New) != want.new {
				t.Errorf("%s: violation %d at %s: old/new = %s/%s, want %s/%s",
					tc.name, i, got.Path, jsonValue(got.Old), jsonValue(got.New), want.old, want.new)
			}
		}
	}
}

func TestFirstReturnsShallowest(t *testing.T) {
	old := `{"a":{"x":{"y":1}},"b":1}`
	new := `{"a":{"x":{"y":2}},"b":2}`
	policy := Policy{Immutable: []string{"a.x.y", "b"}}

	first := First(mustDecode(t, old), mustDecode(t, new), policy)
	if first == nil || first.Path != "b" {
		t.Fatalf("First() = %+v, want violation at b", first)
	}

	clean := First(mustDecode(t, old), mustDecode(t, old), policy)
	if clean != nil {
		t.Fatalf("First() on identical snapshots = %+v, want nil", clean)
	}
}

func TestCompareJSONInvalid(t *testing.T) {
	if _, err := CompareJSON([]byte(`{`), []byte(`{}`), Policy{}); err == nil {
		t.Fatal("CompareJSON with invalid old document: want error")
	}
	if _, err := CompareJSON([]byte(`{}`), []byte(`{`), Policy{}); err == nil {
		t.Fatal("CompareJSON with invalid new document: want error")
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"", "", true},
		{"spec", "spec", true},
		{"spec", "other", false},
		{"spec.replicas", "spec.replicas", true},
		{"spec.replicas", "spec", false},
		{"*", "spec", true},
		{"*", "spec.replicas", false},
		{"spec.*", "spec.replicas", true},
		{"spec.*", "spec", false},
		{"spec.*", "spec.template.replicas", false},
		{"spec.*.replicas", "spec.template.replicas", true},
		{"spec.*.replicas", "spec.a.b.replicas", false},
		{"items.*.id", "items.0.id", true},
		{"items.0.id", "items.*.id", false},
	}
	for _, tc := range cases {
		if got := MatchPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestReasonString(t *testing.T) {
	names := map[Reason]string{
		ReasonModified: "modified",
		ReasonAdded:    "added",
		ReasonRemoved:  "removed",
	}
	for reason, want := range names {
		if !strings.Contains(reason.String(), want) {
			t.Errorf("Reason(%d).String() = %q, want %q", int(reason), reason.String(), want)
		}
	}
}

func mustDecode(t *testing.T, doc string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("invalid test JSON %q: %v", doc, err)
	}
	return v
}

// Guard against accidental aliasing of path segments between
// siblings: paths reported for different branches must not share
// backing arrays.
func TestReportedPathsAreIndependent(t *testing.T) {
	old := `{"a":{"x":1},"b":{"y":2}}`
	new := `{"a":{"x":9},"b":{"y":8}}`
	policy := Policy{Immutable: []string{"a.x", "b.y"}}

	violations := Compare(mustDecode(t, old), mustDecode(t, new), policy)
	paths := make([]string, 0, len(violations))
	for _, v := range violations {
		paths = append(paths, v.Path)
	}
	want := []string{"a.x", "b.y"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}
