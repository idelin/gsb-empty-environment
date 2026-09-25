## delegation-chain

TypeScript implementation of scoped, attenuation-only delegation with chain
proofs and a pure attribute-policy engine. Zero dependencies; runs on the
Node.js built-in test runner (`node:test`) via type stripping.

### Requirements

- Node.js >= 22.6 (developed on v22.14.0, see `.nvmrc`). No `npm install` needed.

### Run

```sh
npm test        # node --test over test/**/*.test.ts
npm run demo    # end-to-end scenario with printed chain proofs
```

### Model

- **Token** (`src/types.ts`): signed record with issuer, subject, finite
  resource/action sets, optional policy, remaining `maxDepth`, a
  `delegatable` flag, a validity window, and a parent link.
- **Keys** (`src/crypto.ts`): HMAC-SHA256 over a canonical JSON payload.
  Keys live in an in-memory registry only — there is no external identity
  service integration by design.
- **Ledger** (`src/ledger.ts`): issues root tokens, mints child delegations,
  revokes tokens, verifies chains, and authorizes concrete accesses.

### Guarantees

- **Attenuation only**: a child cannot widen resources, actions, or the
  validity window of its parent; depth budget decreases every hop.
- **Depth & non-delegatable limits**: `maxDepth` reaches 0 or
  `delegatable: false` and further delegation is rejected at issuance.
- **Cascading expiry**: verification requires *every* ancestor to be inside
  its validity window, so an expired parent kills still-"valid" children.
- **Cascading revocation**: revoking any ancestor invalidates all
  descendants, because verification always walks the full chain.
- **No cycles**: a subject already present in the ancestor chain is
  rejected, so delegation loops can never become a source of power.
- **Chain proof**: `verifyChain`/`authorize` return a JSON proof listing
  every link root-first with each check (signature, revocation, window,
  linkage, attenuation, cycle) and its pass/fail detail.

### Policy engine (`src/policy.ts`)

Policies are pure boolean trees over **explicit attributes only**:

- `in`: membership in a finite set of strings/integers
- `eq`/`lt`/`lte`/`gt`/`gte`: integer comparisons
- `and`/`or`/`not`: boolean composition

Evaluation is a pure function of the attribute bag passed to `authorize`;
missing or wrongly-typed attributes simply make the condition false, and the
proof records exactly which attributes were read.

### Layout

```
src/types.ts      shared types (token, condition, proof)
src/crypto.ts     key registry, HMAC sign/verify, canonical JSON
src/policy.ts     policy validation + pure evaluation
src/ledger.ts     issuance, delegation, revocation, verification, authorize
examples/demo.ts  runnable end-to-end scenario
test/             node:test suites for policy and delegation
```
