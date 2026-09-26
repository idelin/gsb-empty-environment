# Resource Ownership Checker (finite-state models)

Verifies the resource-ownership invariant on a given finite-state model:
**every exit — normal, exceptional (finally), or cancellation — must leave
no acquired resource unreleased**, and every release/transfer must be
performed by the current owner.

## Design

- `model.py` — finite CFG: nodes carry one op (`acquire` / `release` /
  `transfer` / `borrow` / `end_borrow` / `alias` / `set` / `raise` / `nop`);
  edges are labelled `normal` / `except` / `finally` / `cancel` and may
  carry boolean guards.
- `abstract.py` — worklist abstract interpretation. The join points use an
  **explicit abstract domain**: per resource a set of statuses
  `{FREE, RELEASED, OWNED(owner)}` (join = union), plus may-borrows and
  must-aliases. The lattice is finite, so the fixpoint terminates.
  Ownership is tracked **per owner — never as a global reference count** —
  so a move is precise: after `transfer` the old owner can no longer
  release (release-after-move), and a second release is a distinct error
  (double-release). Guards are deliberately not tracked
  (path-insensitive), which is the intended source of false positives.
- `concrete.py` — concrete semantics (ground truth), bounded exhaustive
  exploration, bounded witness search, and trace replay.
- `checker.py` — orchestration. Each abstract candidate is confirmed by
  **bounded concrete replay**:
  - witness found → **definite** finding with a replayable counterexample;
  - replay exhaustive, no witness → false positive, **filtered**;
  - replay bound hit → **possible** finding and the verdict stays
    **pending** (unfinished search is never reported as verified).
- `checker.soundness_check` — reliability cross-check: exhaustively
  enumerate the concrete state graph of a small program and require that
  the abstract analysis flags every concrete violation (no false
  negatives).

## Run

```sh
python3 main.py              # sample models + evidence
python3 -m unittest test_checker -v
```

Requires Python 3.11+, standard library only.
