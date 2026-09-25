"""Core model: versioned facts, rules, derivation steps and a deterministic
forward-chaining engine. All data is synthetic; every artifact carries a
stable ID and a version so evidence can be replayed."""
from __future__ import annotations

import hashlib
from dataclasses import dataclass


def _stable_id(prefix: str, *parts: object) -> str:
    raw = "||".join(str(p) for p in parts)
    return f"{prefix}-{hashlib.sha256(raw.encode('utf-8')).hexdigest()[:12]}"


@dataclass(frozen=True)
class Fact:
    fact_id: str
    key: str
    value: str
    version: int = 1

    def replaced(self, new_value: str) -> "Fact":
        """Return the replacement fact: same ID, bumped version, new value."""
        return Fact(self.fact_id, self.key, new_value, self.version + 1)


@dataclass(frozen=True)
class Rule:
    rule_id: str
    premises: tuple[tuple[str, str], ...]
    conclusion: tuple[str, str]
    version: int = 1


@dataclass(frozen=True)
class Step:
    step_id: str
    rule_id: str
    rule_version: int
    input_fact_ids: tuple[str, ...]
    output_fact: Fact
    version: int = 1


class DerivationError(Exception):
    pass


class Derivation:
    """Immutable result of one forward-chaining run; the replayable evidence."""

    def __init__(self, facts: dict[str, Fact], steps: tuple[Step, ...]):
        self.facts = dict(facts)          # key -> active Fact
        self.steps = tuple(steps)
        self._by_fact_id = {f.fact_id: f for f in self.facts.values()}
        self._producer = {s.output_fact.fact_id: s for s in self.steps}

    # -- queries ---------------------------------------------------------
    def holds(self, key: str, value: str) -> bool:
        fact = self.facts.get(key)
        return fact is not None and fact.value == value

    def support_chain(self, key: str) -> dict:
        """Full provenance for the active fact under `key`: the ordered
        derivation steps plus the source (non-derived) facts they rest on."""
        fact = self.facts.get(key)
        if fact is None:
            raise DerivationError(f"no active fact for key {key!r}")
        ordered_steps: list[Step] = []
        source_facts: list[Fact] = []
        seen: set[str] = set()

        def walk(fid: str) -> None:
            if fid in seen:
                return
            seen.add(fid)
            step = self._producer.get(fid)
            if step is None:
                source_facts.append(self._by_fact_id[fid])
                return
            for parent in step.input_fact_ids:
                walk(parent)
            ordered_steps.append(step)

        walk(fact.fact_id)
        return {
            "goal": fact,
            "steps": ordered_steps,
            "source_facts": source_facts,
        }

    # -- evidence --------------------------------------------------------
    def summary(self) -> str:
        """Content hash of the final fact store and the step trace."""
        parts = sorted(
            f"{k}={f.fact_id}:{f.value}:v{f.version}" for k, f in self.facts.items()
        )
        parts += [s.step_id for s in self.steps]
        return hashlib.sha256("\n".join(parts).encode("utf-8")).hexdigest()

    def replay(self, rules: tuple[Rule, ...]) -> bool:
        """Re-derive from the recorded source facts and verify the evidence
        is identical (same steps, same summary)."""
        sources = tuple(
            f for f in self.facts.values() if f.fact_id not in self._producer
        )
        rerun = derive(sources, rules)
        return (
            rerun.summary() == self.summary()
            and [s.step_id for s in rerun.steps] == [s.step_id for s in self.steps]
        )


def derive(base_facts: tuple[Fact, ...], rules: tuple[Rule, ...]) -> Derivation:
    """Deterministic forward chaining to a fixpoint."""
    store: dict[str, Fact] = {}
    for fact in base_facts:
        if fact.key in store:
            raise DerivationError(
                f"duplicate active fact for key {fact.key!r}: "
                f"{store[fact.key].fact_id} vs {fact.fact_id}"
            )
        store[fact.key] = fact

    steps: list[Step] = []
    changed = True
    while changed:
        changed = False
        for rule in rules:
            matched: list[Fact] = []
            for pkey, pvalue in rule.premises:
                fact = store.get(pkey)
                if fact is None or fact.value != pvalue:
                    break
                matched.append(fact)
            else:
                ckey, cvalue = rule.conclusion
                current = store.get(ckey)
                if current is not None and current.value == cvalue:
                    continue  # already derived / asserted
                inputs = tuple(f.fact_id for f in matched)
                out = Fact(
                    fact_id=_stable_id(
                        "D", rule.rule_id, rule.version, inputs, ckey, cvalue
                    ),
                    key=ckey,
                    value=cvalue,
                    version=1,
                )
                steps.append(
                    Step(
                        step_id=_stable_id("S", rule.rule_id, inputs, out.fact_id),
                        rule_id=rule.rule_id,
                        rule_version=rule.version,
                        input_fact_ids=inputs,
                        output_fact=out,
                    )
                )
                store[ckey] = out
                changed = True
    return Derivation(store, steps)


class Engine:
    """Holds the immutable baseline; interventions never mutate it."""

    def __init__(self, facts: tuple[Fact, ...], rules: tuple[Rule, ...]):
        self._facts = tuple(facts)
        self._rules = tuple(rules)
        self._baseline = derive(self._facts, self._rules)

    @property
    def facts(self) -> tuple[Fact, ...]:
        return self._facts

    @property
    def rules(self) -> tuple[Rule, ...]:
        return self._rules

    def baseline(self) -> Derivation:
        return self._baseline

    def baseline_summary(self) -> str:
        return self._baseline.summary()

    def derive_with(self, facts: tuple[Fact, ...]) -> Derivation:
        return derive(facts, self._rules)

    def intervene(self, *ops: object):
        from .intervention import Intervention, InterventionSession

        return InterventionSession(self, Intervention(ops))
