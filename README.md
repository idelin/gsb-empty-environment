# logtmpl — explainable online log-template clustering

Pure Go standard library. Tests: `go test ./...`. CLI demo:
`go run ./cmd/logtmpl -offline testdata/sample.log`

## Declared policies

**Token rules (explicit, `token.go`).** First match wins:
`NUM` (`200`, `3.14`) → `ERR` (`ERR-503`, `error_42`, `E1234`, `DB-1042`;
family = leading letters, `ERROR`→`ERR`) → `ID` (UUID-like, >=12 hex) →
`WORD`. Other characters (`=`, `,`, ...) become literal `PUNCT` tokens.

**Typed abstraction.** Numbers and IDs generalize to `<NUM>` / `<ID>`.
Error codes generalize only within their family (`ERR-100`+`ERR-200` ->
`<ERR:ERR>`); they never merge with plain numbers or other families
(`E900` stays distinct). Cross-type positions are incompatible, and
templates with different token counts never merge.

**Compatibility distance (`Distance`, public).** Per position: 0 identical,
1 literal absorbed by a wildcard, 2 two literals generalized; incompatible
otherwise. Cluster policy adds: frozen fields, wildcard-ratio cap, split
barriers.

**Forbidden-to-generalize fields.** Position 0 is frozen when it is a WORD
(message-type anchor). Every merge records its evidence: source template
IDs, generalized positions, forbidden positions, distance, sample lines.

**Over-generalization limit.** A merge is rejected if the result's wildcard
ratio exceeds `MaxWildcardRatio` (default 0.5).

**Non-monotonic evolution.** Templates change only through an append-only
journal of `add`/`retract` entries. Generalization = retract + add. When a
wildcard slot observes more than `MaxSlotValues` distinct values, the
template is retracted and split into per-value literals, and a barrier
forbids re-creating the split signature.

**Budget.** `MaxTemplates` caps live templates. Records that cannot be
placed are retained in `Unclassified()` with a reason — never swallowed.

**Determinism policy.** `Deterministic: true` (default) processes records
in canonical sorted order at `Finalize`, so results are independent of
input order. `false` processes in arrival order (streaming). The
independent offline reference (`Recluster`, agglomerative over
canonicalized input) cross-checks small streams; `PartitionOf` compares
record-to-template assignments.

**Telemetry.** Events are synthesized locally in memory only (no monitoring
platform, no dashboards). `WriteFixture`/`ReadFixture` save/load JSONL
fixtures; `ReplayTemplates` reconstructs the live template set from an
event stream, so a fixture replay verifies a run end to end.
