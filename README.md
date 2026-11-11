# jsonpathstream

A streaming JSONPath evaluator for Go collectors that read multi-hundred-MB
JSON responses but only need a handful of fields. Input arrives in arbitrary
blocks, matched values are produced as soon as they appear, and the whole
document is never buffered.

## Supported path syntax

| Feature | Example |
| --- | --- |
| Root | `$` |
| Child member | `$.name`, `$["name"]` |
| Wildcard | `$.*`, `$[*]` |
| Array index | `$[0]`, `$[-1]` |
| Slice (Python-style, positive step) | `$[1:5]`, `$[:10:2]`, `$[3::2]` |
| Recursive descent | `$..name`, `$..*`, `$..[0]` |
| Comparison filter | `$[?(@.price < 10)]`, `$[?(@.a == @.b)]` |

Filter operators: `==`, `!=`, `<`, `<=`, `>`, `>=`. The right-hand side is a
JSON literal (`string`, number, `true`, `false`, `null`) or another
`@`-relative reference. A reference to a missing field is *undefined*, which
makes every comparison (including `!=`) false.

## Usage

```go
p, err := jsonpathstream.Parse(`$.store.book[?(@.price < 10)].title`)
if err != nil { return err }

err = jsonpathstream.EvaluateReader(p, resp.Body, func(m jsonpathstream.Match) error {
    // m.Path  canonical JSONPath, e.g. $.store.book[1].title
    // m.Value float64 / string / bool / nil / Object / []any
    collect(m)
    return nil
})
```

Use `DecodeAll` + `EvaluateAll` to evaluate the same query against a fully
buffered document (the reference implementation).

## Streaming and buffering model

- Scalar matches are emitted to the sink immediately when their token is read.
- A subtree is buffered only when it cannot be resolved online:
  - a **filter** candidate, because a field the predicate references can appear
    later in the same object than the value to output — only the current
    candidate object is held, not the document; or
  - a **negative index/slice**, whose position needs the array length; or
  - a **container selected in full**, whose value must be materialized.
- Peak memory is bounded by the largest individual node, not the document
  size. `TestBoundedMemory` streams a ~12 MB document with effectively zero
  heap growth.

## Correctness

The streaming engine and the whole-document evaluator share the same path
transition and filter logic. The tests verify identical ordered matches for:
hand-written queries covering every feature, 200 randomized documents/queries,
each fed through block sizes of 1, 4 and 33 bytes, plus the late-field filter
and incremental-delivery cases. Run them with:

```sh
go test ./... -race
```

## Not supported

- Root-relative `$` references inside filters (use `@`).
- Negative or zero slice steps (negative start/end bounds are supported).
- Boolean combinations (`&&`, `||`) and nested parenthesized expressions.
