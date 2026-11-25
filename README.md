# Deserialization Admission Package

Importable TypeScript package that decides whether a queued message's type
description graph may be deserialized for a tenant, and certifies that a new
assembly binding table never widens the types an old table was allowed to
accept. Two table releases can coexist: the runtime tries each certified
bundle in order.

Everything crossing the boundary is inert JSON data. No type constructor is
ever invoked; matching is structural, and every traversal is iterative so
inputs deeper than the JS call stack are handled.

## Modules

- `src/types.ts` — public data model and the pinned `RULE_VERSION`.
- `src/canonical.ts` — deterministic canonical JSON, SHA-256 digests and
  constructor identities. Message-side identity (`graphCtorId`) excludes
  variance because messages do not carry variance; the certified catalog
  supplies variance at admission time.
- `src/graph.ts` — normalizes a message graph and enforces the guard rule.
  A cycle is legal only when it crosses at least one structural constructor
  (`named` or `array`). Cycles made only of `ref` edges are bare alias rings
  and are rejected before any language matching.
- `src/grammar.ts` — turns a host finite regular tree grammar plus a binding
  table into a canonical grammar via greatest-fixpoint partition refinement
  (strong bisimulation), so renaming host nonterminals cannot change the
  digest. Arrays are synthetic `array:<rank>` constructors.
- `src/deployment.ts` — validates renames, parameter permutations, explicit
  covariance/contravariance marks and version ranges. An implicit variance
  change is illegal; an open-ended range is illegal; every exact version a
  range admits must already exist in the old table.
- `src/inclusion.ts` — coinductive regular-tree-language inclusion with an
  explicit work budget. `out` slots keep direction, `in` slots flip it,
  `invariant` slots require both directions. Outcomes are `subset`,
  `widened` (with witnesses) or `insufficient` (budget exhausted). An
  exhausted budget can never be published as compatible.
- `src/certificate.ts` — issues and independently verifies inclusion
  certificates. A certificate carries the rule version, both grammar
  digests, the start pair, the full obligation proof, widening witnesses and
  a self-hash. The verifier replays every obligation against the two
  canonical grammars and checks the digest; it needs no deployment secrets.
- `src/runtime.ts` — `admit()` re-checks the certificate digest, the rule
  version and the grammar digest, then performs coinductive membership. State
  is the pair `(node, nonterminal)`, so the same shared node under different
  generic positions is checked against different constraints. The first
  rejected position is returned as an edge path (`args[i]`, `element`,
  `ref->id`).
- `src/unfold.ts` — iterative, depth-limited exhaustive enumeration of all
  finite unfoldings of a graph and of all finite terms of a grammar. Used as
  a test oracle against the coinductive decision.

## Certificate protocol

1. Canonicalize the old and new grammars against their own tables.
2. Project the new table into old-table coordinates using the deployment
   config (renames, permutations, variance marks, exact-version ranges).
   Constructors without an old identity are dropped and recorded as
   `unmatched-production` widening witnesses.
3. Run budgeted coinductive inclusion. The proof lists every obligation used.
4. Hash the certificate body; the `digest` field covers all other fields.

An independent checker calls `verifyCertificate(certificate, leftGrammar,
rightGrammar, ranges)`:

- recomputes the digest,
- checks both grammar digests, the start pair and the budget,
- replays each proof obligation, requiring every constructor match, every
  variance obligation to be present in the proof, and every non-identical
  version to be covered by a configured range,
- confirms the verdict is consistent with its witnesses.

Only a `subset` certificate with a valid digest and matching rule version is
accepted by `admit()`.

## Usage sketch

```ts
import {
  admit,
  issueCertificate,
  canonicalizeGrammar,
  parseVersionRange,
} from "./src/index.ts";

const { certificate, publishable, leftGrammar, rightGrammar, ranges } =
  issueCertificate({ oldTable, newTable, oldGrammar, newGrammar, config });
if (!publishable) {
  // certificate.verdict is "widened" or "insufficient"
}

const result = admit({
  tenant,
  graph: message.typeGraph,
  bundles: [
    { table: oldTable, grammar: rightGrammar, certificate, ranges },
  ],
});
if (!result.admitted) {
  // result.rejection.path is the first rejected type position
}
```

## Tests

Run with the Node built-in test runner (Node 23+, type stripping):

```sh
node --experimental-strip-types --test test/*.test.ts
```

Coverage includes guarded recursive `List`, bare alias rings, covariant vs
invariant positions, contravariant obligation flipping, same-name
different-signature assemblies, renames, permutations, version ranges,
shared nodes in different positions, independently checkable certificates,
budget exhaustion, coexisting tables, and an input 50,000 constructors deep.
Depth-limited full unfoldings are cross-checked against the coinductive
admission decision.
