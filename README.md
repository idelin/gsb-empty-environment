# snapaudit

`snapaudit` compares two structured snapshots (JSON-like trees of
maps, slices and scalars) against an audit policy and reports policy
violations, shallowest path first.

## Paths and wildcards

Paths are dot-separated segments: `spec.containers.0.image`. Slice
indices are ordinary segments. In policy patterns the wildcard `*`
matches **exactly one** segment (a single level); there is no
multi-level wildcard.

```go
snapaudit.MatchPath("spec.containers.*.image", "spec.containers.app.image") // true
snapaudit.MatchPath("spec.containers.*.image", "spec.containers.app")       // false
```

## Policy

```go
type Policy struct {
    Immutable   []string // must be identical in both snapshots
    AllowAdd    []string // new keys/indices permitted here
    AllowDelete []string // removed keys/indices permitted here
}
```

## Comparison semantics

- A difference at or below an immutable path is reported once, at the
  immutable path itself — the shallowest violation path — with the old
  and new values at that path. Deeper duplicates are suppressed.
- Replacing a parent object does **not** bypass protection: the
  replaced subtree is checked recursively for immutable descendants.
- Subtree additions/removals expand recursively: if no rule matches
  the subtree root, each descendant is checked in turn until an
  `AllowAdd`/`AllowDelete` rule matches or a leaf is reached
  (per-leaf expansion).
- Scalar modifications are allowed unless the path is immutable.
- A change of kind (e.g. object → scalar) decomposes into a removal
  of the old subtree plus an addition of the new one.
- Slices are compared index by index.

## Usage

```go
violations, err := snapaudit.CompareJSON(oldJSON, newJSON, snapaudit.Policy{
    Immutable:   []string{"spec.replicas", "spec.containers.*.image"},
    AllowAdd:    []string{"metadata.labels.*"},
    AllowDelete: []string{"spec.paused"},
})
if err != nil { /* invalid JSON */ }
for _, v := range violations {
    fmt.Printf("%s %s: %v -> %v\n", v.Reason, v.Path, v.Old, v.New)
}

// Only the shallowest violation:
first := snapaudit.First(oldValue, newValue, policy)
```

`Compare` accepts already-decoded values (`map[string]interface{}`,
`[]interface{}`, `string`, `float64`, `bool`, `nil` as produced by
`encoding/json`). Violations are ordered shallowest path first.

## Tests

`go test ./...` runs the per-leaf rule-expansion suite in
`diff_test.go`: each case builds both snapshots from JSON, applies a
policy, and checks the exact violation set including old/new values.
