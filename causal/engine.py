"""单调前向链推导引擎：推导到不动点，并为每条结论记录带版本的溯源步骤。"""

from __future__ import annotations

from .model import Fact, FactRef, Step, stable_hash


class Derivation:
    """一次推导的结果：全部事实（断言+派生）与每条派生结论的溯源步骤。"""

    def __init__(self, facts: dict, steps_by_output: dict, asserted_ids: set):
        self.facts = facts
        self.steps_by_output = steps_by_output
        self.asserted_ids = asserted_ids

    def derived_ids(self) -> set:
        return set(self.steps_by_output)

    def steps_for(self, fact_id: str) -> tuple:
        return self.steps_by_output.get(fact_id, ())

    def step_ids_for(self, fact_id: str) -> frozenset:
        return frozenset(s.id for s in self.steps_for(fact_id))

    def cone(self, fact_id: str):
        """结论的溯源锥：(root_fact_ids, rule_refs)，沿步骤递归到断言事实为止。"""
        roots, rules, visited = set(), set(), set()
        stack = [fact_id]
        while stack:
            current = stack.pop()
            for step in self.steps_for(current):
                rules.add((step.rule_id, step.rule_version))
                for ref in step.inputs:
                    if ref in visited:
                        continue
                    visited.add(ref)
                    if ref.id in self.steps_by_output:
                        stack.append(ref.id)
                    else:
                        roots.add(ref.id)
        return roots, rules


def _fire(rule, facts):
    """规则触发时返回 (inputs, output_fact, step)，否则 None。"""
    if not all(cond.matches(facts) for cond in rule.conditions):
        return None
    inputs = tuple(sorted(FactRef(c.fact_id, facts[c.fact_id].version) for c in rule.conditions))
    out_version = int(stable_hash(("fact", rule.id, rule.version, inputs)), 16) % 10**8
    output = Fact(rule.conclusion_id, out_version, rule.conclusion_payload)
    step_id = stable_hash(("step", rule.id, rule.version, inputs, output.id, output.version))
    step = Step(step_id, rule.id, rule.version, inputs, FactRef(output.id, output.version))
    return inputs, output, step


def derive(snapshot) -> Derivation:
    """两阶段推导：先求事实不动点，再在稳定事实上记录全部溯源步骤。

    假设规则集不冲突（同一结论 id 不会被赋予不同载荷），保证确定性。
    """
    facts = dict(snapshot.facts)
    changed = True
    while changed:
        changed = False
        for rule in snapshot.rules.values():
            fired = _fire(rule, facts)
            if fired is None:
                continue
            _, output, _ = fired
            if facts.get(output.id) != output:
                facts[output.id] = output
                changed = True

    steps_by_output = {}
    for rule in snapshot.rules.values():
        fired = _fire(rule, facts)
        if fired is None:
            continue
        _, output, step = fired
        steps = steps_by_output.setdefault(output.id, ())
        if step not in steps:
            steps_by_output[output.id] = steps + (step,)

    return Derivation(facts, steps_by_output, set(snapshot.facts))
