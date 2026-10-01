// Package graphser serializes Go object graphs that contain shared
// references and cycles, and restores them with object identity intact.
//
// Supported serialization scope (anything else is an error at encode time):
//
//   - Scalars: bool, string, all signed/unsigned integers, float32/64.
//   - Structs: exported fields only; unexported fields are skipped.
//   - Pointers: nil allowed. Pointees are identity-bearing objects.
//   - Maps: nil allowed; keys must be scalar (bool/int/uint/float/string).
//     Maps are identity-bearing objects.
//   - Slices and arrays: serialized by value (no identity). A nil slice
//     round-trips as nil; an empty non-nil slice round-trips as empty.
//
// Not supported: interfaces, channels, funcs, unsafe.Pointer, complex
// numbers, struct map keys. Non-finite floats are rejected by encoding/json.
//
// Identity model: pointers and maps carry identity. If two places in the
// graph refer to the same pointer or map, they decode to the same object
// (out.A == out.B). Structs, slices, arrays and scalars are plain values:
// two equal values decode as two independent copies. Self-cycles
// (n.Next = n, m["self"] = m) are preserved.
//
// Wire format: JSON document {version, root, objects}. Every distinct
// pointer/map is assigned a stable object ID in deterministic first-seen
// DFS order (map keys are sorted), so the same graph always encodes to the
// same bytes. References appear as {"kind":"ref","ref":<id>}.
//
// Decode safety: decoding never executes host code. It only builds values
// of types reachable from the caller-supplied output pointer via
// reflect.New/Set; no methods, no UnmarshalJSON hooks, no interface
// instantiation, and type names in the document are merely checked against
// the expected types, never resolved or executed. The whole document is
// validated before any graph memory is allocated: version, duplicate
// object IDs, dangling references, duplicate struct field names, unknown
// kinds, depth and length limits (maxObjects/maxLen/maxDepth) are all
// checked up front, and slice/map capacities are only allocated after
// their lengths have been validated. Documents with dangling IDs,
// duplicate definitions, conflicting types for one ID, or unreferenced
// object definitions are rejected.
package graphser
