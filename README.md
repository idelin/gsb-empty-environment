# Incremental Assembly with Provenance (pure stdlib, Python 3.11)

Local element matrices are assembled into a global system through an
explicit global-dof map. Supports shared nodes, constrained dofs, repeated
contributions, per-entry provenance, element retraction/correction, and
linear elimination of constrained dofs with incremental re-assembly when
constraints change. No NumPy/SciPy; sizes and indices are always explicit.

## Files

- `assembly.py` — `Assembler`, `Contribution`, `assemble_reference`
- `dense.py` — small dense Gaussian-elimination solver for verification
- `example.py` — runnable 1D bar demo (`python3.11 example.py`)
- `test_assembly.py` — unittest suite (`python3.11 -m unittest -v`)

## Design

- **Provenance**: each global entry `(i, j)` keeps `{element_id: value}`;
  the entry value is recomputed with `math.fsum` over only its own sources,
  so updates never traverse other elements.
- **Retract / correct**: `remove_element` / `update_element` subtract the
  stored contribution and re-add the new one; only touched entries change.
- **Constraint elimination**: for `u_d = c`, apply `b_j -= A_jd * c` to
  every coupled row, then replace row/column `d` with the identity and set
  `b_d = c`. Corrections are recorded per constraint.
- **Constraint versions**: `set_constraints` bumps a version counter; the
  old elimination is undone (rows/columns restored from the unconstrained
  base system, recorded rhs corrections rolled back) and the new one is
  re-applied incrementally. Provenance reports the applied version.
- **Atomic batches**: `add_elements` validates every mapping first; any
  invalid index or shape rejects the whole batch with no state change.
- **Cross-check**: `assemble_reference` is an independent from-scratch
  full assembly used by the tests to validate every incremental path.

## Test results

`python3.11 -m unittest -v` — 15 tests, all OK:

- shared dof contributions add; sign cancellation sums to zero
- repeated dof inside one map accumulates
- constraint elimination applies the rhs correction (`b_2: 1.0 -> 1.5`)
- constraint change undo + incremental re-assembly matches full assembly
- remove / update element matches independent full assembly
- invalid mapping / shape / duplicate id rejects the whole batch
- provenance reports element sources and constraint versions
