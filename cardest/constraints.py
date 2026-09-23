"""Statistics as integer count constraints; consistency checking; MUS.

Every statistic compiles to one or more linear constraints of the form
    sum of multiplicities over a subset of cells == target
where a "cell" is one possible tuple of a relation (domain values plus
NULL per column). Consistency of a statistic set is decided by searching
for any satisfying assignment (no solver, plain propagation + DFS).
When inconsistent, a minimal conflict subset (MUS) is computed by
deletion: removing any single member of the returned subset yields a
consistent set.
"""
from __future__ import annotations

from itertools import product

from .search import Searcher
from .spec import relation_column_names, relation_domains


def enumerate_cells(spec, relations):
    """All cells of the given relations.

    Returns (cells, cell_rel, cell_tuple) where cells[i] is the global cell
    index, cell_rel[i] the relation name, cell_tuple[i] the value tuple
    (None = NULL) in the relation's column order.
    """
    cell_rel = []
    cell_tuple = []
    for rel in relations:
        domains = relation_domains(spec, rel)
        # NULL is an implicit extra value for every column
        value_sets = [sorted(d) + [None] for d in domains]
        for tup in product(*value_sets):
            cell_rel.append(rel)
            cell_tuple.append(tup)
    return cell_rel, cell_tuple


def compile_constraints(spec, relations, stat_ids=None):
    """Compile statistics into (cells, constraints) for the given relations.

    constraints: list of (cell_index_list, target, stat_id).
    stat_ids: optional set restricting which statistics are included.
    """
    cell_rel, cell_tuple = enumerate_cells(spec, relations)
    rel_offset = {}
    for i, rel in enumerate(cell_rel):
        rel_offset.setdefault(rel, i)
    col_index = {
        rel: {c: j for j, c in enumerate(relation_column_names(spec, rel))}
        for rel in relations
    }
    constraints = []
    for st in spec.get("statistics", []):
        if stat_ids is not None and st["id"] not in stat_ids:
            continue
        rel = st["relation"]
        if rel not in relations:
            continue
        base = rel_offset[rel]
        ncells_rel = sum(1 for r in cell_rel if r == rel)
        cidx = col_index[rel]
        stype = st["type"]
        if stype == "row_count":
            cells = list(range(base, base + ncells_rel))
            constraints.append((cells, st["count"], st["id"]))
        elif stype == "histogram":
            col = st["column"]
            j = cidx[col]
            counts = {int(k): v for k, v in st["counts"].items()}
            for value in relation_domains(spec, rel)[j]:
                target = counts.get(value, 0)  # omitted values mean zero
                cells = [
                    base + off
                    for off in range(ncells_rel)
                    if cell_tuple[base + off][j] == value
                ]
                constraints.append((cells, target, st["id"]))
        elif stype == "null_count":
            j = cidx[st["column"]]
            cells = [
                base + off
                for off in range(ncells_rel)
                if cell_tuple[base + off][j] is None
            ]
            constraints.append((cells, st["count"], st["id"]))
        elif stype == "joint_count":
            jcols = st["columns"]
            js = [cidx[c] for c in jcols]
            given = {tuple(e["values"]): e["count"] for e in st["counts"]}
            domains = [relation_domains(spec, rel)[j] for j in js]
            # complete over non-null value combinations: omitted combos = 0
            combos = [tuple(vs) for vs in product(*[sorted(d) for d in domains])]
            combos += [k for k in given if any(v is None for v in k)]
            for combo in combos:
                target = given.get(combo, 0)
                cells = [
                    base + off
                    for off in range(ncells_rel)
                    if all(cell_tuple[base + off][j] == v for j, v in zip(js, combo))
                ]
                constraints.append((cells, target, st["id"]))
    return (cell_rel, cell_tuple), constraints


def check_consistency(spec, relations=None, stat_ids=None, budget=100000):
    """Decide whether the (sub)set of statistics is satisfiable.

    Returns a dict with status 'consistent' | 'inconsistent' | 'unknown'
    (unknown only if the node budget was exhausted).
    """
    if relations is None:
        relations = sorted(
            {s["relation"] for s in spec["statistics"] if stat_ids is None or s["id"] in stat_ids}
        )
        if not relations:
            return {"status": "consistent", "witness": None}
    (cell_rel, cell_tuple), constraints = compile_constraints(spec, relations, stat_ids)
    searcher = Searcher(len(cell_rel), [(c, t) for c, t, _ in constraints])
    result = searcher.run(budget)
    if result["status"] == "feasible":
        return {
            "status": "consistent",
            "witness": result["witness"],
            "nodes_explored": result["nodes_explored"],
        }
    if result["status"] == "infeasible":
        return {"status": "inconsistent", "nodes_explored": result["nodes_explored"]}
    return {"status": "unknown", "nodes_explored": result["nodes_explored"]}


def find_minimal_conflict(spec, budget=100000):
    """Find a minimal unsatisfiable subset (MUS) of the statistics.

    Returns None if the full set is consistent. Otherwise returns a dict:
      conflict: list of statistic ids (minimal: removing any one of them
                makes the remaining set consistent -- each such removal is
                re-verified and reported in 'removal_checks').
    """
    all_ids = [s["id"] for s in spec["statistics"]]
    full = check_consistency(spec, stat_ids=set(all_ids), budget=budget)
    if full["status"] == "consistent":
        return None
    if full["status"] == "unknown":
        return {"status": "unknown"}
    current = list(all_ids)
    for sid in list(current):
        trial = [x for x in current if x != sid]
        res = check_consistency(spec, stat_ids=set(trial), budget=budget)
        if res["status"] == "inconsistent":
            current = trial
    # Certificate: removing any single member of the MUS is consistent.
    removal_checks = []
    for sid in current:
        trial = [x for x in current if x != sid]
        res = check_consistency(spec, stat_ids=set(trial), budget=budget)
        removal_checks.append({"removed": sid, "status": res["status"]})
    return {
        "status": "inconsistent",
        "conflict": current,
        "removal_checks": removal_checks,
        "minimal": all(c["status"] == "consistent" for c in removal_checks),
    }
