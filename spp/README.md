# spp — BGP Stable Paths Problem & Dispute Wheel Toolkit

`spp` is a self-contained Go library (standard library only) that analyzes the
control plane of inter-domain routing for one destination prefix `d`. It
implements the Griffin–Wilfong **Stable Paths Problem (SPP)** on top of an AS
graph with Gao–Rexford commercial relationships.

## Capabilities

1. **Export filtering & candidate expansion** — enumerates every simple,
   loop-free path to `d`, applies AS_PATH loop prevention and the Gao–Rexford
   valley-free export rule (routes learned from a peer or provider are never
   re-exported to a non-customer), and detects directed customer→provider
   commercial cycles.
2. **Exact Stable Path Assignment (SPA) solver** — enumerates *all* fixed
   points where each AS selects its highest-preference path among the routes
   currently advertised by its neighbors, and classifies the instance as:
   - `OutcomeUnique` — safe convergence to one SPA;
   - `OutcomeMultiple` — at least two mutually exclusive SPAs (timing-dependent
     split);
   - `OutcomeNoStable` — no SPA exists (permanent oscillation).
3. **Dispute wheel construction** — builds concrete wheels of distinct pivot
   ASes `u_i`, spoke paths `Q_i` and rim paths `R_i` whose first hop is
   `u_{i+1}`, emitting a machine-checkable `R_i >_i Q_i` rank proof per step.
4. **Minimum repair synthesis** — searches action combinations in increasing
   cardinality (demote one local-pref ranking, or disable one non-core export
   edge), preserving prefix reachability, and re-verifies that the repaired
   instance has zero wheels and exactly one SPA.

## Path and relationship model

Paths are oriented from the choosing AS toward the origin: `path[0]` is the
choosing AS and `path[len-1]` is the origin. Links are:

- `RelationCustomerToProvider` — link names the customer first (`A` customer,
  `B` provider);
- `RelationPeer` — lateral settlement-free peering;
- `RelationUnknown` — unrestricted link for abstract gadgets (exempt from
  valley checks).

A valley-free path has shape `up* (peer?) down*`: zero or more
customer→provider edges, at most one peer edge, then zero or more
provider→customer edges.

## Quick start

```go
g, _ := spp.NewASGraph(nodes, links)
inst, _ := spp.Build(g, spp.PolicyConfig{Origin: d, StrictGR: true})

analysis := spp.Analyze(inst, spp.SolveOptions{}, 8)
fmt.Println(analysis.Solve.Outcome)        // unique / multiple / none
for _, w := range analysis.Wheels { ... }  // pivot/Q/R + R_i >_i Q_i proofs

plans, _ := inst.MinimumRepair(spp.RepairOptions{AllowEdgeDisable: true})
// plans[0].Verify.Outcome == OutcomeUnique, zero remaining wheels
```

Classic gadgets (Bad Gadget, DISAGREE) supply their permitted paths
explicitly via `PolicyConfig.ExtraPaths` with `ExplicitOnly: true`, preserving
the caller's strict preference order instead of re-sorting by local-pref.

## JSON input

`spp.LoadSpec` / `spp.BuildFromSpec` import declarative instances; see
`../testdata/bad_gadget.json` and `../testdata/valley_free.json`.

## Demo

```
go run ./cmd/sppdemo              # all four acceptance scenarios
go run ./cmd/sppdemo bad-gadget   # one scenario
```

## Acceptance scenarios

| Test | Instance | Expected result |
| --- | --- | --- |
| `TestBadGadgetNoStableState` | 3-AS Bad Gadget | no SPA, 3-pivot wheel |
| `TestBadGadgetMinimumRepair` | repaired Bad Gadget | size-1 repair → unique SPA |
| `TestDisagree4TwoStableStates` | 4-AS DISAGREE | two mutually exclusive SPAs, 2-pivot wheel |
| `TestValleyFreeNetworkUniqueStable` | clean customer/provider tree | clean GR audit, unique SPA + lock-in proof |
| `TestBackupLinkBreaksValleyFreeAndTriggersWheel` | engineered backup local-pref | GR violation + hidden wheel → minimum repair |
| `TestCommercialCycleDetected` | cyclic customer/provider claims | commercial cycle reported |
| `TestCoreEdgesProtectedFromRepair` | strict GR tree | no core edge offered for disabling |
| `TestIllegalExportDetected` | peer re-export of provider route | illegal-export violation |
| `TestASPathLoopRejected` | repeated AS in path | loop violation, path excluded |
