"""NAIVE projection by plain field deletion - INSECURE, demo only.

This module exists solely to provide the counterexample required by the
security model: deleting hidden *fields* while keeping array slots,
premise counts, support depths and derivation-wide totals still leaks
information about hidden facts.  The pairwise noninterference tester in
``noninterference.py`` detects the leak; the fixed projection in
``projection.py`` passes the same tests.  Do not use this module.
"""
from __future__ import annotations

from .engine import Derivation
from .projection import ExplanationError, PUBLIC_ERROR


def naive_project(derivation: Derivation, query_key: str, clearance: int) -> dict:
    fact = derivation.facts_by_key.get(query_key)
    if fact is None or fact.level > clearance:
        raise ExplanationError(PUBLIC_ERROR)

    depth_memo: dict[str, int] = {}

    def support_depth(f) -> int:
        if f.fact_id in depth_memo:
            return depth_memo[f.fact_id]
        step = derivation.producer.get(f.key)
        if step is None:
            depth = 0
        else:
            depth = 1 + max(
                (support_depth(derivation.facts_by_id[i]) for i in step.inputs),
                default=0,
            )
        depth_memo[f.fact_id] = depth
        return depth

    def walk_fact(f) -> dict:
        if f.level > clearance:
            # LEAK: the depth of the hidden support chain is exposed.
            return {"type": "deleted", "hidden_support_depth": support_depth(f)}
        node = {"type": "fact", "fact_id": f.fact_id, "version": f.version,
                "key": f.key, "value": f.value}
        step = derivation.producer.get(f.key)
        if step is None:
            node["support"] = None
            return node
        rule = derivation.rules_by_id[step.rule_id]
        if rule.level > clearance:
            node["support"] = {"type": "deleted",
                               "hidden_support_depth": support_depth(f)}
            return node
        # LEAK: one slot per hidden premise (count) and the total premise
        # count including hidden inputs are both exposed.
        children = [walk_fact(derivation.facts_by_id[i]) for i in step.inputs]
        node["support"] = {
            "type": "step",
            "step_id": step.step_id,
            "rule_id": step.rule_id,
            "premise_count": len(step.inputs),
            "children": children,
        }
        return node

    summary = {
        # LEAK: derivation-wide totals include hidden facts and steps.
        "total_facts_in_derivation": len(derivation.facts_by_key),
        "total_steps_in_derivation": len(derivation.steps),
        "hidden_fact_count": sum(
            1 for f in derivation.facts_by_key.values() if f.level > clearance),
        "hidden_step_count": sum(
            1 for s in derivation.steps
            if derivation.rules_by_id[s.rule_id].level > clearance
            or derivation.facts_by_id[s.output].level > clearance),
    }
    return {"query": query_key, "clearance": clearance,
            "tree": walk_fact(fact), "summary": summary}
