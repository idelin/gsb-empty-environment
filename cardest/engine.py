"""Analysis engine: consistency check, exact/bounded search, certificates."""
from __future__ import annotations

from .constraints import check_consistency, compile_constraints, find_minimal_conflict
from .query import validate_query_predicate
from .search import JoinObjective, Searcher, SelectObjective, StaleStateError
from .spec import relation_column_names, spec_fingerprint

DEFAULT_MAX_CELLS = 512
DEFAULT_NODE_BUDGET = 200000


def analyze(spec, node_budget=None, max_cells=None, resume_state=None):
    """Run the full analysis. Returns a JSON-serializable result dict."""
    validate_query_predicate(spec)
    config = spec.get("config", {})
    if node_budget is None:
        node_budget = config.get("node_budget", DEFAULT_NODE_BUDGET)
    if max_cells is None:
        max_cells = config.get("max_cells", DEFAULT_MAX_CELLS)
    fingerprint = spec_fingerprint(spec)
    result = {
        "spec_fingerprint": fingerprint,
        "node_budget": node_budget,
    }

    if resume_state is not None:
        saved_fp = resume_state.get("spec_fingerprint")
        if saved_fp != fingerprint:
            raise StaleStateError(
                "saved search state was produced under different statistics; "
                "previous bounds are invalid and cannot be resumed"
            )

    # 1. consistency of the statistics
    cons = check_consistency(spec, budget=node_budget)
    if cons["status"] == "unknown":
        result["status"] = "consistency_unknown"
        return result
    if cons["status"] == "inconsistent":
        mus = find_minimal_conflict(spec, budget=node_budget)
        result["status"] = "inconsistent"
        result["conflict"] = mus
        return result
    result["consistency"] = {"status": "consistent"}

    # 2. build the search problem for the query's relations
    query = spec["query"]
    if query["type"] == "select":
        relations = [query["from"]]
    else:
        relations = [query["left"], query["right"]]
    (cell_rel, cell_tuple), constraints = compile_constraints(spec, relations)
    ncells = len(cell_rel)
    result["problem"] = {
        "relations": relations,
        "cells": ncells,
        "constraints": len(constraints),
    }
    if ncells > max_cells:
        result["status"] = "domain_too_large"
        result["note"] = (
            f"{ncells} cells exceed configured limit {max_cells}; "
            "raise config.max_cells to attempt an exact search"
        )
        return result

    columns = [relation_column_names(spec, r) for r in relations]
    if query["type"] == "select":
        objective = SelectObjective(
            list(zip(cell_rel, cell_tuple)), columns[0], query.get("where")
        )
    else:
        objective = JoinObjective(
            list(zip(cell_rel, cell_tuple)),
            relations,
            columns,
            query["on"],
            query.get("where"),
        )
    searcher = Searcher(ncells, [(c, t) for c, t, _ in constraints], objective)
    search_result = searcher.run(node_budget, state=resume_state)
    result["search"] = {
        "nodes_explored": search_result["nodes_explored"],
        "nodes_pruned": search_result["nodes_pruned"],
    }

    def pack_witness(assign):
        return _witness_json(relations, columns, cell_rel, cell_tuple, assign)

    if search_result["status"] == "exact":
        result["status"] = "exact"
        result["exact"] = True
        lo, hi = search_result["proven_lower"], search_result["proven_upper"]
        result["bounds"] = {"lower": lo, "upper": hi, "kind": "proven"}
        result["min"] = {
            "value": search_result["best_min"][0],
            "witness": pack_witness(search_result["best_min"][1]),
        }
        result["max"] = {
            "value": search_result["best_max"][0],
            "witness": pack_witness(search_result["best_max"][1]),
        }
    elif search_result["status"] == "budget_exhausted":
        result["status"] = "budget_exhausted"
        result["exact"] = False
        result["note"] = (
            "node budget exhausted; bounds below are proven to cover every "
            "unexplored branch, but the exact optimum is NOT known"
        )
        result["bounds"] = {
            "lower": search_result["proven_lower"],
            "upper": search_result["proven_upper"],
            "kind": "proven",
        }
        for key, best in (("min", search_result["best_min"]), ("max", search_result["best_max"])):
            if best is not None:
                result[key] = {"value": best[0], "witness": pack_witness(best[1])}
            else:
                result[key] = None
        state = search_result["state"]
        state["spec_fingerprint"] = fingerprint
        result["resume_state"] = state
    else:  # infeasible should have been caught by the consistency check
        result["status"] = "inconsistent"  # pragma: no cover
    return result


def _witness_json(relations, columns, cell_rel, cell_tuple, assign):
    """Expand a cell-multiplicity assignment into a relation witness."""
    out = {}
    col_of = dict(zip(relations, columns))
    for i, (rel, tup) in enumerate(zip(cell_rel, cell_tuple)):
        count = assign.get(i, 0)
        if count:
            out.setdefault(rel, []).append(
                {
                    "tuple": {c: v for c, v in zip(col_of[rel], tup)},
                    "count": count,
                }
            )
    return out
