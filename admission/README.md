# admission — round-admission accounting

Import path: `prescreen.local/ddd-first-pass-beacon/admission`.

This package quantifies selective abstention in a multi-party
commitment/reveal audit that assigns completed rounds to independent
reviewers. The set of rounds that actually complete is not an unconditional
random sample, because participants decide to publish or abstain after seeing
a public prefix.

## Inputs (public data only)

A `Model` contains only:

- signed-commitment `Identity` values and each participant's public partition
  (`Participant.VisibleMessages`);
- public `MessageType` values;
- exact rational `ProbabilityTable` values (`math/big.Rat`);
- the rule-allowed abstention position on each decision node;
- the ideal allocation `Model.Ideal` and the public retry limit
  `Model.MaxRetries` (<= 2).

There is no field for private key material.

## Determinism guarantees enforced at `Compile`

- One decision variable per `(subject, public observation)`. Two secret
  states that present the same public prefix collapse onto one variable, so a
  rule cannot branch on hidden state.
- Subjects sharing a failure domain share one variable per observation; the
  host must pin the choice explicitly in `Model.HostConditions`.
- Retries only traverse edges marked `Retry` into dedicated entry nodes, on
  paths consuming at most `MaxRetries`. A named `FixedVariable` has exactly
  one draw node, so a fixed random quantity is structurally never re-drawn.
- The round graph is an acyclic DAG and may share successor states.

## Outputs

- `Enumerate` evaluates every deterministic rule (at most
  `2^MaxDecisionPoints = 16384`) with exact rationals and returns, per
  category, the worst conditional deviation from the ideal allocation, plus a
  tight failure (incomplete-round) probability bound.
- `Certificate` contains only the model digest, an exhaustive-record hash
  chain root and the resulting bounds. It never carries an extremal policy or
  any manipulation recipe. `Certificate.Verify` re-compiles and
  re-enumerates independently.

## Tests

`go test ./...` covers conditional abstention, normalization over successful
rounds, one-observation/many-secret-state merging, retry without re-drawing
fixed quantities, failure-domain coupling, certificate tamper detection,
exact rational arithmetic and a 2,400-node DAG enumeration.
