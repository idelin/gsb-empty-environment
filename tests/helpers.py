"""Shared helpers for the test-suite (synthetic data only)."""

from __future__ import annotations

import hashlib

from merkle_evidence.model import EvidenceLog, EvidenceRecord, Fact, Rule, Step
from merkle_evidence.proofs import Checkpoint
from merkle_evidence.tree import MerkleTree

VERSION_A = "log-v1"
VERSION_B = "log-v2"


def make_digests(n: int, tag: str = "rec") -> list[bytes]:
    return [hashlib.sha256(f"{tag}-{i}".encode()).digest() for i in range(n)]


def make_tree(n: int, version: str = VERSION_A, tag: str = "rec") -> MerkleTree:
    return MerkleTree(make_digests(n, tag), version)


def make_checkpoint(n: int, version: str = VERSION_A,
                    tag: str = "rec") -> Checkpoint:
    tree = make_tree(n, version, tag)
    return Checkpoint(version, n, tree.root())


def make_record(i: int, fact_version: int = 1) -> EvidenceRecord:
    fact = Fact(f"FACT-{i:04d}", fact_version, f"synthetic observation #{i}")
    rule = Rule(f"RULE-{i:04d}", 1, f"synthetic rule #{i}")
    step = Step(f"STEP-{i:04d}", 1, "collect",
                fact_refs=((fact.fact_id, fact.version),),
                rule_refs=((rule.rule_id, rule.version),))
    return EvidenceRecord(f"REC-{i:04d}", 1, facts=(fact,), rules=(rule,),
                          steps=(step,))


def make_log(n: int, version: str = VERSION_A) -> EvidenceLog:
    return EvidenceLog("synthetic-log", version,
                       tuple(make_record(i + 1) for i in range(n)))
