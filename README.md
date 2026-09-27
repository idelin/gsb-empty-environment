# Element Constraint: `value = array[index]`

Pure-stdlib (Python 3.11) GAC propagator + DFS solver for the element
constraint. No SAT/SMT libraries.

## Files
- `element.py` — `Variable`, `ElementConstraint`, `Store` (trail + undo +
  explanations), `Solver` (GAC fixpoint + DFS), `check_element` (independent
  checker), `enumerate_solutions` (brute-force ground truth), demo `main`.
- `test_element.py` — 19 unittests.

## Design notes
- **Aliasing**: variables are identified by object identity. The propagator
  enumerates tuples over the *unique* variable set `{index, value, array
  vars}`, so `index`/`value`/array entries aliasing the same variable keep
  their correlation exactly (never treated as independent domains).
- **GAC**: a value stays iff it appears in some valid tuple
  (`0 <= idx < len(array)` and `value == array[idx]`). Out-of-bounds index
  values are pruned with an explicit "out of bounds" reason.
- **Explanations**: every removal records an `Explanation` (constraint,
  variable, value, reason, snapshot of the other variables' domains).
- **Undo**: `Store.save()/restore(mark)` replays the trail backwards and
  drops explanations of restored values.
- **Verification**: `solve()` results are checked by `check_element`, which
  shares no propagation code with the solver; tests also cross-check support
  sets against `enumerate_solutions` over the initial domains.
- Empty array ⇒ every index value is out of bounds ⇒ wipeout ⇒ UNSAT.
  Unique index ⇒ value-domain and selected-element-domain prune each other
  bidirectionally (falls out of GAC). Multiple candidate indices never force
  all elements equal to `value`.

## Run
```
python3.11 element.py            # demo
python3.11 -m unittest test_element -v
```

## Demo output (excerpt)
```
constraint: val = [x, 4, y][idx]
after GAC propagation (ok = True):
  idx = [0, 1, 2]   val = [1, 2, 4, 5]   x = [1, 2]   y = [2, 5]
explanations for removed values:
  [element] idx /= 3: index 3 out of bounds for array of size 3 | ...
  [element] idx /= 7: index 7 out of bounds for array of size 3 | ...
  [element] val /= 3: no supporting tuple under current domains (GAC) | ...
under branch idx=0: val = [1, 2]  x = [1, 2]
after undo: idx = [0, 1, 2]  val = [1, 2, 4, 5]  x = [1, 2]
solution found: {'idx': 0, 'val': 1, 'x': 1, 'y': 2}
independent checker verifies solution: True
=== Aliasing demo: i = [i, 10][i] ===
after propagation: i = [0, 1]  v = [0, 10]
```

## Test result
```
Ran 19 tests in 0.002s
OK
```
Covers: index self-reference (`v = [i,10][i]`, `v = [i][i]`), value aliasing
an array element, duplicate element variables, out-of-bounds pruning with
reasons, out-of-bounds branch backtracking with undo, unique-index
bidirectional pruning, multi-candidate non-unification, empty-array UNSAT,
explanation completeness, and support-set/solution equivalence with
brute-force enumeration.
