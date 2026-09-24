# reversible-text-patch

Reversible, context-validated text patches in dependency-free TypeScript.
Runs on Node.js ≥ 22.6 (native type stripping); tests use the built-in
`node:test` runner. No build step, no runtime dependencies.

## Model

A patch is a sequence of ops consumed left-to-right against a base text.
All offsets and counts are **UTF-16 code units** (matching JavaScript string
indexing); operation boundaries may never split a surrogate pair.

```ts
type Op =
  | { kind: "retain"; count: number }              // copy base → output
  | { kind: "delete"; count: number; text?: string } // skip base; `text` is context
  | { kind: "insert"; id: string; text: string };   // emit text, unique identity

interface Patch { baseLength: number; baseDigest: string; ops: Op[] }
```

- **Context validation** — every patch records `baseLength` (UTF-16) and
  `baseDigest` (SHA-256 over `utf16:<len>:` + UTF-8 bytes). A base with the
  same length but different content can never validate. Delete ops may
  additionally carry their expected content, checked on apply.
- **Atomic apply** — the digest is validated up front and the result is
  built in memory; any failure throws `PatchApplyError` and produces no
  partial result. `TextDocument.apply` commits only after full success.
- **Insert identity** — every insert carries a unique op id, so identical
  text inserted twice is never mistaken for one edit; concurrent inserts at
  the same position are ordered deterministically by id.

## Operations

| Function | Meaning |
| --- | --- |
| `diffPatch(base, edited)` | generate a patch (prefix/suffix diff, pair-safe cuts) |
| `applyPatch(base, patch)` | validated, atomic application |
| `invertPatch(base, patch)` | inverse patch; re-inserts get fresh identities |
| `composePatches(p, q)` | `apply(base, p∘q) === apply(apply(base, p), q)` |
| `transformOps(a, b)` | OT transform of concurrent ops |
| `rebasePatch(base, a, b)` | three-way relocation: rebase `a` onto `apply(base, b)` |

**Inverse-op elimination in compose** happens in the streaming walk, so the
coordinates later ops refer to stay anchored to the same origin:

- `a` inserts text that `b` deletes → insert/delete cancel (fully or partially);
- `a` deletes content that `b` re-inserts verbatim (e.g. `b = inverse(a)`)
  → the pair collapses to a retain, using the delete op's carried content.

**Conflicts** — overlapping (crossing) deletions in concurrent patches throw
`ConflictError`. Disjoint edits commute: the swap diagram
`apply(apply(s,a), b′) === apply(apply(s,b), a′)` is verified in tests.

## Invariants (tested)

- `apply(invert(P))` after `P` restores the **byte-exact** original.
- `P∘Q` applied once equals `P` then `Q` applied sequentially.
- Same-length, different-content baselines fail digest validation.
- Composition, inversion and failure atomicity are cross-checked against a
  stepwise (op-at-a-time) reference implementation on randomized inputs
  with repeated text, emoji (surrogate pairs), LF and CRLF newlines.

## Usage

```bash
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts
```

```ts
import { diffPatch, applyPatch, invertPatch, composePatches, rebasePatch } from "./src/index.ts";

const p = diffPatch("hello 😀\r\n", "hello there 😀\r\n");
applyPatch("hello 😀\r\n", p);            // "hello there 😀\r\n"
const inv = invertPatch("hello 😀\r\n", p);
applyPatch("hello there 😀\r\n", inv);    // byte-exact original
```
