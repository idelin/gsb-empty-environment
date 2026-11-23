# type-admission

Importable deserialization admission package for the TypeScript gateway.
A delayed-delivery queue may carry messages built against either of two
assembly binding tables; this package decides, per tenant, whether the type
graph in a message is inside the language the tenant was ever allowed to
receive, and whether a newly deployed binding table is safe to publish.

## Threat model and rules

- Messages carry **type descriptor graphs only**: shared nodes identified by
  string ids, each node holding assembly public key, exact four-part version,
  type name, generic arguments, array rank, or recursive references.
- The host describes the admitted language per tenant as a **finite regular
  tree grammar** of guarded constructor alternatives, with covariant,
  contravariant, and invariant generic positions.
- New deployments may configure type renames, generic parameter permutations,
  variance markers, and version intervals. Old permissions are never enlarged:
  the new table is accepted only when its accepted tree set is a subset.
- A recursive reference is protected only when the cycle crosses an admitted
  container constructor. Bare alias rings are rejected.
- Silently widening variance across versions (e.g. invariant to covariant) is
  illegal; variance markers of a rename must match the old grammar exactly.
- Assemblies with the same simple/type name but different public keys are
  different assemblies and never inherit each other's permissions.

## Modules

- `src/types.ts` — JSON-serializable data model (no classes cross the wire).
- `src/version.ts` — four-part version ordering, interval containment,
  assembly predicate matching.
- `src/graph.ts` — iterative graph validation, dangling-reference and bare
  alias-ring detection with the tenant's own container predicate.
- `src/grammar.ts` — guardedness checking and policy compilation: renames,
  bijection-checked permutations, variance enforcement, interval narrowing.
- `src/inclusion.ts` — coinductive, variance-aware containment check with a
  pair budget; emits `subset`, `widened` (with counterexample), or `unknown`.
- `src/verify.ts` — independent certificate verifier that replays every proof
  edge instead of trusting the issuer's verdict.
- `src/runtime.ts` — stack-safe admission gate; memoizes by
  `(node id, nonterminal)` so one shared node can pass at one generic
  position and fail at another; returns the first rejected position.

## Inclusion certificates

`certifyInclusion(newGrammar, oldGrammar, budget)` returns a certificate:

- `subset` — proof lists every explored `(newNt, oldNt)` pair, the matching
  old alternative for each new alternative, and the oriented argument pairs.
- `widened` — includes a concrete counterexample location.
- `unknown` — the pair budget was exhausted. An over-budget result is never
  published as compatible and cannot gate runtime admission.

Certificates pin the rule version (`type-admission/rules/v1`) and carry
canonical SHA-256 hashes of both normalized grammars. `verifyCertificate`
re-checks hashes, head containment, variance, argument orientation, proof
coverage, and the start pair.

## Runtime use

```ts
import {
  admit,
  certifyInclusion,
  compileBaseline,
  compileNewDeployment,
  verifyCertificate,
} from './src/index.ts';

const old = compileBaseline(policy);
const next = compileNewDeployment(policy);
const certificate = certifyInclusion(next, old, 10_000);

// Deployment gate: publish only when an independent verifier agrees.
if (!verifyCertificate(certificate, next, old).valid) {
  throw new Error('binding table failed containment review');
}

// Per-message admission on the queue worker.
const result = admit(message.typeGraph, next, certificate);
if (!result.admitted) {
  // result.rejection.path is the first rejected type position.
}
```

The gate never invokes constructors or methods on message data; nodes are
inert records. All traversal is iterative with parent-pointer frames, so
input depth can exceed the JavaScript call stack.

## Tests

```sh
node --experimental-strip-types --test test/admission.test.ts test/verify.test.ts
```

Coverage includes recursive `List`, covariant vs. invariant positions,
same-name/different-signature assemblies, shared nodes at different
positions, bare alias rings, renames and permutations, variance relaxation
rejection, interval containment, budget overflow, rule-version/grammar hash
pinning, constructor-call prohibition (Proxy trap), depth-limited full
unfoldings cross-checked against the coinductive gate, and a 200k-deep input.
