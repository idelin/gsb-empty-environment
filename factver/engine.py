"""Derivation steps, precise invalidation and replayable evidence."""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any, Dict, List, Tuple

from .model import DATA, REMARK, WILDCARD
from .rules import FactView, FieldRead, RuleRegistry
from .store import CorrectionError, FactStore


def _canonical(obj: Any) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


@dataclass(frozen=True)
class InputRef:
    name: str
    fact_id: str
    version: int


@dataclass(frozen=True)
class DerivationStep:
    """One recorded rule execution. Stable step_id, increasing version."""

    step_id: str
    version: int
    rule_id: str
    rule_version: int
    inputs: Tuple[InputRef, ...]
    reads: Tuple[FieldRead, ...]
    output: Any
    evidence_hash: str


@dataclass(frozen=True)
class ReplayResult:
    step_id: str
    ok: bool
    detail: str
    expected_hash: str
    actual_hash: str


def _evidence_hash(rule_id, rule_version, inputs, reads, output) -> str:
    payload = {
        "rule": [rule_id, rule_version],
        "inputs": [[i.name, i.fact_id, i.version] for i in inputs],
        "reads": [[r.fact_id, r.version, r.kind, r.field] for r in reads],
        "output": output,
    }
    return hashlib.sha256(_canonical(payload).encode("utf-8")).hexdigest()


class Engine:
    """Runs rules against the store, tracks reads, invalidates, replays."""

    def __init__(self, store: FactStore, registry: RuleRegistry) -> None:
        self.store = store
        self.registry = registry
        self._steps: Dict[str, List[DerivationStep]] = {}

    # -- derivation ----------------------------------------------------
    def run(self, rule_id: str, inputs: Dict[str, str],
            rule_version: int = None, step_id: str = None) -> DerivationStep:
        rule = self.registry.get(rule_id, rule_version)
        snapshots = {name: self.store.get(fid) for name, fid in inputs.items()}
        reads: List[FieldRead] = []
        views = {name: FactView(snap, reads.append)
                 for name, snap in snapshots.items()}
        output = rule.func(views)
        try:
            json.dumps(output)
        except (TypeError, ValueError) as exc:
            raise CorrectionError(
                f"rule output is not JSON-serializable: {exc}") from None
        input_refs = tuple(sorted(
            (InputRef(name, snap.fact_id, snap.version)
             for name, snap in snapshots.items()),
            key=lambda i: i.name,
        ))
        read_set = tuple(sorted(set(reads)))
        if step_id is None:
            bound = ",".join(f"{i.name}={i.fact_id}" for i in input_refs)
            step_id = f"{rule.rule_id}[{bound}]"
        version = len(self._steps.get(step_id, ())) + 1
        step = DerivationStep(
            step_id=step_id,
            version=version,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            inputs=input_refs,
            reads=read_set,
            output=output,
            evidence_hash=_evidence_hash(
                rule.rule_id, rule.version, input_refs, read_set, output),
        )
        self._steps.setdefault(step_id, []).append(step)
        return step

    def steps(self) -> List[DerivationStep]:
        return [hist[-1] for hist in self._steps.values()]

    def get_step(self, step_id: str, version: int = None) -> DerivationStep:
        hist = self._steps[step_id]
        return hist[-1] if version is None else hist[version - 1]

    # -- invalidation --------------------------------------------------
    def is_stale(self, step: DerivationStep) -> bool:
        """A step is stale iff some field it actually read changed since."""
        for read in step.reads:
            if self.store.current_version(read.fact_id) == read.version:
                continue
            changes = self.store.changes_since(read.fact_id, read.version)
            if read.kind == DATA:
                if read.field == WILDCARD:
                    if changes.data_fields:
                        return True
                elif read.field in changes.data_fields:
                    return True
            elif read.kind == REMARK and read.field in changes.remarks:
                return True
        return False

    def stale_steps(self) -> List[str]:
        return [s.step_id for s in self.steps() if self.is_stale(s)]

    # -- replay --------------------------------------------------------
    def replay(self, step_id: str, version: int = None) -> ReplayResult:
        """Re-execute the recorded rule version against the recorded fact
        versions (history is retained) and compare with the evidence."""
        step = self.get_step(step_id, version)
        try:
            rule = self.registry.get(step.rule_id, step.rule_version)
            snapshots = {i.name: self.store.get(i.fact_id, i.version)
                         for i in step.inputs}
            reads: List[FieldRead] = []
            views = {name: FactView(snap, reads.append)
                     for name, snap in snapshots.items()}
            output = rule.func(views)
        except Exception as exc:
            return ReplayResult(step.step_id, False, f"replay raised: {exc!r}",
                                step.evidence_hash, "")
        read_set = tuple(sorted(set(reads)))
        actual = _evidence_hash(step.rule_id, step.rule_version,
                                step.inputs, read_set, output)
        problems = []
        if read_set != step.reads:
            problems.append("read set differs")
        if output != step.output:
            problems.append("output differs")
        if actual != step.evidence_hash:
            problems.append("evidence hash differs")
        return ReplayResult(
            step.step_id, not problems,
            "replayed OK" if not problems else "; ".join(problems),
            step.evidence_hash, actual,
        )
