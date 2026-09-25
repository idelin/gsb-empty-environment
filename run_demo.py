#!/usr/bin/env python3
"""Runnable synthetic scenario for the evidence-driven entity store.

Scenarios:
  1. A=B, B=C merges three records into one entity (representative = min id).
  2. Asserting A!=C closes a contradictory evidence cycle; the cycle carries
     every explicit fact id so it can be replayed.
  3. Retracting B=C splits the component back into two entities.
  4. Two different records sharing the same display name never auto-merge.
  5. The JSONL event log is replayed from scratch into a second repository,
     producing identical derived state (same stable ids and versions).
"""

from __future__ import annotations

import json
from pathlib import Path

from entity_kg import EntityRepository, FactKind

DATA_DIR = Path(__file__).parent / "demo_data"
LOG_PATH = DATA_DIR / "events.jsonl"

STEPS: list[str] = []


def section(title: str) -> None:
    print("\n" + "=" * 72)
    print(title)
    print("=" * 72)


def show_state(repo: EntityRepository) -> None:
    print(f"derived state version: {repo.state.version}")
    print("entities:")
    for entity in repo.state.entities:
        labels = ", ".join(f"{m}={repo.labels.get(m, '?')}"
                           for m in entity.members)
        print(f"  - representative={entity.representative:<8} members="
              f"{entity.members}  [{labels}]")
    steps = repo.state.steps
    print(f"derived steps: {len(steps)}")
    for step in steps:
        premises = " -> ".join(p.fact_id[:18] for p in step.chain)
        print(f"  {step.sid[:22]} v{step.version} [{step.rule_id}] "
              f"{step.a}={step.b} via {premises}")
    conflicts = repo.state.conflicts
    print(f"conflicts: {len(conflicts)}")
    for conflict in conflicts:
        print(f"  {conflict.cid[:22]} kind={conflict.kind} "
              f"rule={conflict.rule_id} neq={conflict.neq_fact_id[:18]}")
        for edge in conflict.cycle:
            link = "==" if edge.kind is FactKind.EQ else "!="
            print(f"      {edge.a} {link} {edge.b}  fact={edge.fact_id[:18]} "
                  f"step={(edge.step_id or '-')[:18]}")


def main() -> None:
    if LOG_PATH.exists():
        LOG_PATH.unlink()
    DATA_DIR.mkdir(exist_ok=True)

    section("STEP 1  explicit evidence A=B, B=C (synthetic records)")
    repo = EntityRepository(LOG_PATH)
    repo.add_name("rec-a", "张三")
    repo.add_name("rec-b", "张三（旧档）")
    repo.add_name("rec-c", "Zhang San")
    r1 = repo.assert_eq("rec-a", "rec-b",
                        reason="same government id 310000-19900101-001X")
    r2 = repo.assert_eq("rec-b", "rec-c",
                        reason="manual review #8823 matched phone +1-555-0100")
    print(f"fact {r1.fact_id[:22]} v1  rec-a == rec-b")
    print(f"fact {r2.fact_id[:22]} v1  rec-b == rec-c")
    show_state(repo)

    section("STEP 2  assert A != C  -> contradictory evidence cycle")
    r3 = repo.assert_neq(
        "rec-a", "rec-c",
        reason="biometric check #77 failed (different fingerprint)")
    print(f"fact {r3.fact_id[:22]} v1  rec-a != rec-c")
    print("conflict ids reported by the operation:",
          [c[:22] for c in r3.conflict_ids])
    show_state(repo)

    section("STEP 3  retract edge B=C -> components split apart again")
    ret = repo.retract("rec-b", "rec-c", FactKind.EQ,
                       reason="review #8823 overturned: phone reused")
    print(f"event seq={ret.seq} RETRACT fact={ret.fact_id[:22]}")
    show_state(repo)

    section("STEP 4  same display name does NOT imply same entity")
    repo2 = EntityRepository()
    repo2.add_name("rec-x", "李四")
    repo2.add_name("rec-y", "李四")
    print("rec-x and rec-y are both named '李四', with zero EQ evidence:")
    show_state(repo2)
    print("representative(rec-x) =", repo2.representative("rec-x"))
    print("representative(rec-y) =", repo2.representative("rec-y"))

    section("STEP 5  replay JSONL log into a fresh repository")
    replayed = EntityRepository.replay(LOG_PATH)
    fresh = json.dumps(replayed.state.to_dict(), sort_keys=True,
                       ensure_ascii=False)
    original = json.dumps(repo.state.to_dict(), sort_keys=True,
                          ensure_ascii=False)
    print("log file:", LOG_PATH)
    print("replayed events:", len(replayed.events))
    print("derived state identical after replay:", fresh == original)
    facts_match = all(
        replayed.facts[k].to_dict() == v.to_dict()
        for k, v in repo.facts.items())
    print("fact ids / versions identical after replay:", facts_match)
    print("\nraw log:")
    print(LOG_PATH.read_text(encoding="utf-8").strip())

    section("RULE / FACT / STEP STABLE ID & VERSION REGISTRY")
    for rule in repo.rules():
        print(f"  rule {rule.rid:<18} v{rule.version}  {rule.name}")
    print("\nfacts (post-retraction):")
    for fact in sorted(repo.facts.values(), key=lambda f: f.added_seq):
        status = "ACTIVE" if fact.active else "RETRACTED"
        print(f"  {fact.fid[:22]} v{fact.version} {fact.kind} "
              f"{fact.a}~{fact.b} {status} reason={fact.reason}")


if __name__ == "__main__":
    main()
