"""Facts, rules and derivation steps.

Every fact, rule and step carries a stable ID and a version. Rules are pure,
versioned functions so a derivation log can be deterministically replayed.
"""
from __future__ import annotations

import json
from dataclasses import dataclass


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    value: float

    def to_dict(self) -> dict:
        return {"fact_id": self.fact_id, "version": self.version,
                "value": self.value}

    @classmethod
    def from_dict(cls, body: dict) -> "Fact":
        return cls(fact_id=body["fact_id"], version=body["version"],
                   value=body["value"])


def _rule_mean_v1(values: list[float]) -> float:
    return round(sum(values) / len(values), 6)


def _rule_max_v1(values: list[float]) -> float:
    return max(values)


def _rule_min_v1(values: list[float]) -> float:
    return min(values)


RULE_REGISTRY: dict[tuple[str, int], object] = {
    ("mean", 1): _rule_mean_v1,
    ("max", 1): _rule_max_v1,
    ("min", 1): _rule_min_v1,
}


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    inputs: tuple[Fact, ...]
    output: Fact

    def to_dict(self) -> dict:
        return {
            "step_id": self.step_id,
            "version": self.version,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "inputs": [fact.to_dict() for fact in self.inputs],
            "output": self.output.to_dict(),
        }

    @classmethod
    def from_dict(cls, body: dict) -> "Step":
        return cls(
            step_id=body["step_id"],
            version=body["version"],
            rule_id=body["rule_id"],
            rule_version=body["rule_version"],
            inputs=tuple(Fact.from_dict(item) for item in body["inputs"]),
            output=Fact.from_dict(body["output"]),
        )

    def run(self) -> Fact:
        rule = RULE_REGISTRY.get((self.rule_id, self.rule_version))
        if rule is None:
            raise UnknownRule(self.rule_id, self.rule_version)
        return Fact(fact_id=self.output.fact_id,
                    version=self.output.version,
                    value=rule([fact.value for fact in self.inputs]))


class UnknownRule(Exception):
    def __init__(self, rule_id: str, rule_version: int):
        super().__init__(f"unknown rule {rule_id}@v{rule_version}")


def encode_log(steps: list[Step]) -> bytes:
    lines = [json.dumps(step.to_dict(), sort_keys=True, separators=(",", ":"))
             for step in sorted(steps, key=lambda s: s.step_id)]
    return ("\n".join(lines) + "\n").encode("utf-8")


def decode_log(data: bytes) -> list[Step]:
    steps = []
    for line in data.decode("utf-8").splitlines():
        if line.strip():
            steps.append(Step.from_dict(json.loads(line)))
    return steps


def replay(steps: list[Step]) -> list[dict]:
    """Re-run every step deterministically; returns one failure per mismatch."""
    failures = []
    for step in sorted(steps, key=lambda s: s.step_id):
        try:
            recomputed = step.run()
        except UnknownRule:
            failures.append({"code": "UNKNOWN_RULE", "step_id": step.step_id,
                             "detail": f"{step.rule_id}@v{step.rule_version}"})
            continue
        if recomputed != step.output:
            failures.append({
                "code": "REPLAY_MISMATCH",
                "step_id": step.step_id,
                "detail": f"recorded={step.output.value!r} replay={recomputed.value!r}",
            })
    return failures
