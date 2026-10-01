# Auditable CSP Solver (Python 3.11, stdlib only)

A finite-domain CSP solver that records a fully justified **event trace**
(decisions, propagations, conflicts, backtracks), plus an **independent
replay checker** that re-executes the trace from the original problem and
verifies every step against the proof rules. A tampered trace is rejected
at the **first illegal event**. No SAT/SMT libraries are used.

## Files

- `csp.py` — model (table constraints), `has_support`, independent `check_solution`
- `trace.py` — event definitions (JSON-friendly)
- `solver.py` — backtracking + GAC propagation, emits the trace; status is `SAT` / `UNSAT` / `UNKNOWN`
- `checker.py` — `check_trace`: independent replay verifier
- `demo.py` — runnable examples (SAT, UNSAT, tamper, budget)
- `test_csp.py` — 18 unittest cases

## Proof rules enforced by the replay checker

- `decide` — value must be in the current domain of a non-singleton variable;
  one branch variable per level; no value tried twice.
- `propagate` — the deleted value must currently be in the domain **and** have
  no support in the cited constraint. *Unjustified domain deletions fail.*
- `conflict` — the cited domain must actually be empty.
- `backtrack` — pops exactly one checkpoint; the restored state must equal the
  snapshot recorded in the event (*snapshot restore is verified*).
- `exhausted` — every value of the branch variable at that level was tried.
- `solution` — all domains singleton and matching the claim; the assignment is
  re-validated against the **original** constraints by `check_solution`
  (complete assignment required — no missing/extra variables, values from the
  original domains).
- `unsat` — only after the root level was exhausted or a root conflict was
  derived. An inconclusive (`unknown`) trace can **never** be certified as
  UNSAT.

## Run

```sh
python3.11 demo.py          # sample runs
python3.11 -m unittest test_csp -v
```

## Sample output (abridged)

```
=== 1. SAT instance: 3-coloring a small map ===
  #000 decide: {'var': 'NT', 'value': 'B'}
  #001 propagate: {'var': 'WA', 'value': 'B', 'constraint': 0}
  ...
  #007 solution: {'assignment': {'WA': 'G', 'NT': 'B', 'SA': 'R', 'Q': 'G'}}
  trace check : ok=True certified=SAT (trace verified)
  independent solution check: ok=True (ok)

=== 2. UNSAT instance: 2-coloring a triangle ===
  ...
  #012 exhausted: {'var': 'A'}
  #013 unsat: {}
  trace check : ok=True certified=UNSAT (trace verified)

=== 3. Tampered trace: first illegal event is located ===
  tampered event #1: #001 propagate: {'var': 'WA', 'value': 'G', 'constraint': 0}
  trace check : ok=False first_bad=1
  reason      : unjustified deletion: WA='G' still has support in constraint #0

=== 4. Budget-limited search: UNKNOWN, never wrapped as UNSAT ===
  solver says : UNKNOWN (last event: #003 unknown: {'reason': 'event budget exhausted'})
  forged 'unsat' trace: ok=False first_bad=3
```

## Test results

```
Ran 18 tests in 0.001s
OK
```
