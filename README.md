# cfgmerge

Layered deployment-config merging with provenance, modelled as flat
dot-separated leaf paths.

## Merge semantics

Layers apply in order; later layers win.

- **Objects** merge recursively, key by key.
- **Arrays** replace wholesale (an array is an opaque leaf).
- **Scalars and nulls** replace whatever was there; `nil` keeps the key.
- **`Delete`** (sentinel) removes the key and its whole subtree.
- **Type switches replace**: a leaf set over an object discards the
  subtree; an object merged over a leaf discards the leaf.

Empty objects contribute no keys. `Delete` inside an array is not
interpreted. Keys must not contain `.`.

## Provenance

- `Source(path)` — layer that set the final leaf value.
- `Overridden(path)` — layers that set the path earlier and lost, oldest first.
- `Explain(path)` — human-readable fate of any path: source and overridden
  chain for existing leaves; for missing paths, whether it was never set,
  deleted (and by which layer, including no-op repeated deletes), or removed
  when a parent was replaced by a leaf / deleted / regrown as an object.

## Example

```go
m := cfgmerge.NewMerger()
m.Apply(cfgmerge.Unflatten(map[string]any{"a.b": 1, "a.c": 2})) // layer 0
m.Apply(cfgmerge.Unflatten(map[string]any{"a.b": cfgmerge.Delete, "a.c": nil})) // layer 1

m.Flatten()      // map[a.c:<nil>]
m.Explain("a.b") // "a.b was deleted at layer 1; previously set by layer(s) 0"
```

## Tests

`merge_test.go` builds layers with `Unflatten` and asserts against the
flattened model, covering recursive object merge, array replacement,
null-vs-delete, type switches (object↔leaf↔array), repeated and never-set
deletes, subtree deletion, and parent-object replacement followed by
regrowth.

```sh
go test ./...
```
