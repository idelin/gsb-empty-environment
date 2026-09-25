"""Replayable evidence: export a DAG plus its canonical summary as JSON,
rebuild it from scratch and verify that the canonical form reproduces."""

from __future__ import annotations

from .canon import CanonicalProof, canonicalize, default_hash
from .dag import ProofDAG
from .model import Fact, Rule, Step

FORMAT = "proofdag-evidence/1"


def export_evidence(dag: ProofDAG, proof: CanonicalProof) -> dict:
    rules: dict[tuple, dict] = {}
    for step in dag.steps.values():
        rules[(step.rule.stable_id, step.rule.version)] = {
            "stable_id": step.rule.stable_id,
            "version": step.rule.version,
            "name": step.rule.name,
            "commutative": step.rule.commutative,
        }
    return {
        "format": FORMAT,
        "facts": [
            {
                "stable_id": f.stable_id,
                "version": f.version,
                "source_id": f.source_id,
                "payload": f.payload,
            }
            for _, f in sorted(dag.facts.items())
        ],
        "rules": [rules[k] for k in sorted(rules)],
        "steps": [
            {
                "node_id": s.node_id,
                "stable_id": s.stable_id,
                "version": s.version,
                "rule": [s.rule.stable_id, s.rule.version],
                "premises": list(s.premises),
                "conclusion": {
                    "stable_id": s.conclusion.stable_id,
                    "version": s.conclusion.version,
                    "source_id": s.conclusion.source_id,
                    "payload": s.conclusion.payload,
                },
            }
            for _, s in sorted(dag.steps.items())
        ],
        "goals": sorted(dag.goals),
        "canonical_form": proof.canonical_form,
        "summary": proof.summary,
    }


def replay(evidence: dict, hash_fn=default_hash) -> CanonicalProof:
    """Rebuild the DAG from evidence and recompute its canonical proof."""
    if evidence.get("format") != FORMAT:
        raise ValueError(f"unsupported evidence format {evidence.get('format')!r}")
    rules = {
        (r["stable_id"], r["version"]): Rule(
            stable_id=r["stable_id"],
            version=r["version"],
            name=r["name"],
            commutative=r["commutative"],
        )
        for r in evidence["rules"]
    }
    dag = ProofDAG()
    for f in evidence["facts"]:
        dag.add_fact(
            Fact(
                stable_id=f["stable_id"],
                version=f["version"],
                source_id=f["source_id"],
                payload=f["payload"],
            )
        )
    for s in evidence["steps"]:
        rule = rules[tuple(s["rule"])]
        c = s["conclusion"]
        dag.add_step(
            Step(
                node_id=s["node_id"],
                stable_id=s["stable_id"],
                version=s["version"],
                rule=rule,
                premises=tuple(s["premises"]),
                conclusion=Fact(
                    stable_id=c["stable_id"],
                    version=c["version"],
                    source_id=c["source_id"],
                    payload=c["payload"],
                ),
            )
        )
    for goal in evidence["goals"]:
        dag.add_goal(goal)
    return canonicalize(dag, hash_fn=hash_fn)


def verify(evidence: dict, hash_fn=default_hash) -> bool:
    """Replay the evidence and check the canonical form and summary match."""
    proof = replay(evidence, hash_fn=hash_fn)
    return (
        proof.summary == evidence.get("summary")
        and proof.canonical_form == evidence.get("canonical_form")
    )
