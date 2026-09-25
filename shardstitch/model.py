"""核心数据模型：事实、规则、步骤、假设、证明与分片。

所有实体都带稳定 ID 与版本号；摘要通过对规范化 JSON 做 SHA-256 得到，
保证同一份证据在任何机器上重放后得到相同摘要。
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass

FactRef = tuple[str, int]  # (fact_id, version)


def canonical(obj: object) -> str:
    """确定性序列化：键排序、无空白，保证摘要可重放。"""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def digest_of(obj: object) -> str:
    return hashlib.sha256(canonical(obj).encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    payload: str

    @property
    def ref(self) -> FactRef:
        return (self.fact_id, self.version)

    def to_obj(self) -> dict:
        return {
            "kind": "fact",
            "id": self.fact_id,
            "version": self.version,
            "payload": self.payload,
        }


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    op: str
    output_fact_id: str
    output_version: int

    @property
    def ref(self) -> tuple[str, int]:
        return (self.rule_id, self.version)

    @property
    def output_ref(self) -> FactRef:
        return (self.output_fact_id, self.output_version)

    def to_obj(self) -> dict:
        return {
            "kind": "rule",
            "id": self.rule_id,
            "version": self.version,
            "op": self.op,
            "output": [self.output_fact_id, self.output_version],
        }


@dataclass(frozen=True)
class Assumption:
    """边界假设：本分片需要其它分片为 (fact_id, fact_version) 提供证据，
    且该证据的摘要必须等于 expected_digest。"""

    assumption_id: str
    fact_id: str
    fact_version: int
    claimed_payload: str
    expected_digest: str

    @property
    def ref(self) -> FactRef:
        return (self.fact_id, self.fact_version)

    def to_obj(self) -> dict:
        return {
            "kind": "assumption",
            "id": self.assumption_id,
            "fact": [self.fact_id, self.fact_version],
            "claimed_payload": self.claimed_payload,
            "expected_digest": self.expected_digest,
        }


@dataclass(frozen=True)
class Step:
    """一步推导：应用某版本的规则，把输入事实引用映射为输出事实引用。"""

    step_id: str
    version: int
    rule_id: str
    rule_version: int
    inputs: tuple[FactRef, ...]
    output: FactRef

    def to_obj(self) -> dict:
        return {
            "kind": "step",
            "id": self.step_id,
            "version": self.version,
            "rule": [self.rule_id, self.rule_version],
            "inputs": [list(ref) for ref in self.inputs],
            "output": list(self.output),
        }


@dataclass(frozen=True)
class Proof:
    """一条结论的完整证据：用到的局部事实、边界假设与推导步骤。"""

    proof_id: str
    version: int
    conclusion: FactRef
    facts: tuple[Fact, ...] = ()
    assumptions: tuple[Assumption, ...] = ()
    steps: tuple[Step, ...] = ()

    def to_obj(self) -> dict:
        return {
            "kind": "proof",
            "id": self.proof_id,
            "version": self.version,
            "conclusion": list(self.conclusion),
            "facts": sorted((f.to_obj() for f in self.facts), key=canonical),
            "assumptions": sorted((a.to_obj() for a in self.assumptions), key=canonical),
            "steps": [s.to_obj() for s in self.steps],
        }

    def digest(self) -> str:
        return digest_of(self.to_obj())


@dataclass(frozen=True)
class Shard:
    shard_id: str
    version: int
    rules: tuple[Rule, ...] = ()
    proofs: tuple[Proof, ...] = ()

    def to_obj(self) -> dict:
        return {
            "kind": "shard",
            "id": self.shard_id,
            "version": self.version,
            "rules": sorted((r.to_obj() for r in self.rules), key=canonical),
            "proofs": sorted((p.to_obj() for p in self.proofs), key=canonical),
        }

    def content_digest(self) -> str:
        return digest_of(self.to_obj())
