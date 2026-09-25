# pairwise-tms: minimal justification-based truth maintenance

Python 3.11, standard library only, tests via unittest.

## Semantics

- **Facts** are assumptions with stable fact_id + version; the fact's
  proposition is the node it grounds. Re-asserting an identical fact is a
  no-op (noop=True on the step) and never inflates support counts.
- **Rules** carry stable rule_id + version and encode conjunctive
  premises: premises -> conclusion. Multiple rules may conclude the same
  node (alternative supports).
- A node is **IN** iff it is in the *least* set containing all asserted
  propositions and closed under the rules. Least-fixed-point semantics means
  a rule cycle with no external fact support cannot sustain its conclusions.
- The **support set** of an IN node is the minimal set of currently valid
  justifications: assumption if asserted, plus one entry per rule
  whose premises are all IN. Stale supports are dropped on every retraction.
- Every mutation is recorded as a **Step** with stable id, version, and the
  SHA-256 hash of the resulting state. Engine.replay() re-applies the log
  on a fresh engine and verifies every recorded hash, so evidence is
  replayable and tamper-evident.

## Run

    python3.11 sample.py
    python3.11 -m unittest discover -s tests -v

## Layout

- tms/core.py — Fact, Rule, Step, Engine, ReplayReport
- sample.py — synthetic demo: A&B->C, D->C, retraction cascade, cycle
- tests/test_tms.py — 14 unittest cases
