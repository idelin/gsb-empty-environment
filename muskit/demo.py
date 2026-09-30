"""Sample run: subset-minimal vs minimum-cardinality core on a tiny problem.

Conflict structure (x in {0,1}, y in {0,1,2}):

    a: x == 0      b: x == 1
    c: y != 0      d: y != 1      e: y != 2

* {a, b}    is the unique minimum-cardinality core (size 2);
* {c, d, e} is a subset-minimal core of size 3 (any two of c/d/e are
            satisfiable, all three are not).

Deletion-based extraction in order a..e removes a and b first (the set stays
UNSAT thanks to c/d/e) and ends at {c, d, e}: subset-minimal but NOT minimum
cardinality.  The hitting-set search then proves {a, b} optimal with a
disjoint-correction-set lower bound.
"""
from __future__ import annotations

from .cache import CachedSolver
from .constraints import Constraint, Problem, Variable
from .core import extract_subset_minimal_core
from .exhaustive import (
    classify_all_subsets,
    recovery_consistency,
    verify_minimum_cardinality_core,
    verify_subset_minimal_core,
)
from .hitting import find_min_cardinality_core
from .solver import BacktrackingSolver


def build_problem() -> Problem:
    variables = [Variable("x", (0, 1)), Variable("y", (0, 1, 2))]
    constraints = [
        Constraint("a", ("x",), lambda x: x == 0, "x == 0"),
        Constraint("b", ("x",), lambda x: x == 1, "x == 1"),
        Constraint("c", ("y",), lambda y: y != 0, "y != 0"),
        Constraint("d", ("y",), lambda y: y != 1, "y != 1"),
        Constraint("e", ("y",), lambda y: y != 2, "y != 2"),
    ]
    return Problem(variables, constraints)


def _hr(title: str) -> None:
    print()
    print("=" * 68)
    print(title)
    print("=" * 68)


def main() -> None:
    problem = build_problem()
    solver = CachedSolver(problem, BacktrackingSolver(problem))
    ids = problem.constraint_ids()

    _hr("Problem")
    for v in problem.variables.values():
        print(f"  var {v.name} in {list(v.domain)}")
    for c in problem.constraints.values():
        print(f"  {c.cid}: {c.description}")

    _hr("1. Deletion-based subset-minimal core (unlimited budget)")
    result = extract_subset_minimal_core(problem, solver.solve, ids)
    for rec in result.log:
        trial = "{" + ",".join(rec.trial) + "}"
        print(f"  try removing {rec.cid}: solve {trial:<14} -> {rec.trial_status:<7}"
              f" ({rec.nodes} nodes) => {rec.decision}")
    print(f"  core = {result.core}  status = {result.status}")
    for cid, witness in sorted(result.witnesses.items()):
        print(f"  deletion witness for {cid}: core\\{{{cid}}} satisfied by {witness}")

    _hr("2. Minimum-cardinality core via hitting-set search")
    mc = find_min_cardinality_core(problem, solver.solve, ids)
    for line in mc.log:
        print(f"  {line}")
    print(f"  best core = {mc.best_core}  status = {mc.status}")
    print(f"  lower bound (disjoint correction sets) = {mc.lower_bound},"
          f" upper bound = {mc.upper_bound}, proven = {mc.proven}")
    print(f"  correction sets found: {mc.correction_sets}")
    print(f"  => subset-minimal core {result.core} (size {len(result.core)}) is NOT"
          f" minimum-cardinality; optimum is {mc.best_core} (size {mc.upper_bound})")

    _hr("3. Budget exhaustion propagates as UNDECIDED")
    tight = extract_subset_minimal_core(problem, solver.solve, ids, budget_per_check=3)
    for rec in tight.log:
        trial = "{" + ",".join(rec.trial) + "}"
        print(f"  try removing {rec.cid}: solve {trial:<14} -> {rec.trial_status:<7}"
              f" ({rec.nodes} nodes) => {rec.decision}")
    print(f"  budget_per_check=3 -> status = {tight.status} (core kept as {tight.core})")
    print("  note: kept-undecided items block the SUBSET_MINIMAL claim;")
    print("        UNKNOWN was not treated as UNSAT (no item removed on UNKNOWN)")
    tight2 = find_min_cardinality_core(problem, solver.solve, ids,
                                       budget_per_check=3, candidate_budget=2)
    for line in tight2.log:
        print(f"  {line}")
    print(f"  hitting search, candidate_budget=2 -> status = {tight2.status},"
          f" proven = {tight2.proven}, best = {tight2.best_core}")

    _hr("4. Cache bound to assumption-set version")
    before = (solver.hits, solver.misses)
    solver.solve(ids)          # cached from step 1's initial check
    solver.solve(["a", "b"])   # cached from step 2
    print(f"  hits/misses before: {before}, after two repeated solves:"
          f" hits={solver.hits}, misses={solver.misses}")
    problem.add_constraint(Constraint("f", ("x",), lambda x: x >= 0, "x >= 0"))
    misses_before = solver.misses
    solver.solve(ids)          # revision bumped -> miss even though ids identical
    print(f"  constraint pool mutated (revision -> {problem.revision});"
          f" same ids solved again: misses {misses_before} -> {solver.misses}")
    problem.remove_constraint("f")

    _hr("5. Exhaustive verification (all 2^5 subsets)")
    classification = classify_all_subsets(problem, solver.solve, ids)
    smu = sorted(sorted(s) for s in classification.subset_minimal_unsat())
    print(f"  subsets: {len(classification.unsat_sets)} UNSAT,"
          f" {len(classification.sat_sets)} SAT, {len(classification.unknown_sets)} unknown")
    print(f"  subset-minimal UNSAT sets: {smu}")
    print(f"  minimum-cardinality cores: "
          f"{sorted(sorted(s) for s in classification.minimum_cardinality_cores())}")
    ok, msg = verify_subset_minimal_core(result.core, classification)
    print(f"  [{ 'OK' if ok else 'FAIL'}] {result.core} subset-minimal: {msg}")
    ok, msg = verify_minimum_cardinality_core(result.core, classification)
    print(f"  [{'OK' if ok else 'FAIL'}] {result.core} minimum-cardinality: {msg}")
    ok, msg = verify_minimum_cardinality_core(mc.best_core, classification)
    print(f"  [{'OK' if ok else 'FAIL'}] {mc.best_core} minimum-cardinality: {msg}")
    ok, msg = recovery_consistency(problem, solver.solve, result.core, ids,
                                   extract_subset_minimal_core)
    print(f"  [{'OK' if ok else 'FAIL'}] recovery consistency: {msg}")


if __name__ == "__main__":
    main()
