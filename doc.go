// Package netsec implements key-expression authorization for a Zenoh router.
//
// A subscription is authorized only when its complete key set is contained by
// one allow rule and is disjoint from every deny rule. A publish key is treated
// as a singleton and must be contained by an allow rule and not matched by any
// deny rule. Access is denied by default.
//
// Besides one non-empty value per segment, key expressions support:
//
//   - "*" for exactly one non-empty chunk;
//   - "**" for zero or more chunks;
//   - "$*" for any number of non-slash characters inside a chunk.
package netsec
