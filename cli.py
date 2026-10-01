#!/usr/bin/env python3
"""Command line interface for the flat and tree DRR schedulers.

Usage:
    python3.11 cli.py run SCENARIO.json     # run a flat/tree scenario
    python3.11 cli.py check STARVE.json     # run the starvation checker

Output: one JSON object per line (JSONL) on stdout, describing every
scheduler event (credit / dequeue with deficit evidence / reject /
pause / resume / migrate / snapshot ...). Exit code 0 on success.

Scenario format (mode "flat"):
    {
      "mode": "flat",
      "quantum": 2,
      "deficit_cap": 5,
      "tenants": [{"id": "A", "weight": 1, "cap": 5,
                   "tasks": [["t1", 5], ["t2", 3]]}],
      "operations": [
        {"op": "run_rounds", "n": 3},
        {"op": "set_weight", "tenant": "A", "weight": 2},
        {"op": "pause",  "tenant": "A"},
        {"op": "resume", "tenant": "A"},
        {"op": "enqueue", "tenant": "A", "task_id": "t3", "cost": 4},
        {"op": "run_until_idle", "max_rounds": 50}
      ]
    }

Scenario format (mode "tree"):
    {
      "mode": "tree",
      "nodes": [
        {"id": "g1", "parent": "root", "weight": 1, "quantum": 4, "cap": 8},
        {"id": "A", "parent": "g1", "weight": 1, "quantum": 3, "cap": 6,
         "leaf": true, "tasks": [["a1", 2], ["a2", 5]]}
      ],
      "operations": [
        {"op": "run_rounds", "n": 2},
        {"op": "pause",  "node": "g1"},
        {"op": "resume", "node": "g1"},
        {"op": "set_weight", "node": "A", "weight": 2},
        {"op": "enqueue", "node": "A", "task_id": "a3", "cost": 3},
        {"op": "migrate", "leaf": "A", "new_parent": "g2"},
        {"op": "snapshot"},
        {"op": "run_until_idle", "max_rounds": 50}
      ]
    }

Starvation-check format (cli.py check):
    {
      "nodes": [...same as tree mode...],
      "streams": {"B": [6, 3]},
      "victim_leaf": "A",
      "victim_cost": 6,
      "state_bound": 100000
    }
"""
from __future__ import annotations

import json
import sys

from drr import FlatDRR
from starvation import check_starvation
from tree_drr import TreeDRR


def emit(ev: dict) -> None:
    print(json.dumps(ev, ensure_ascii=False, sort_keys=True))


def apply_op(sched, op: dict, mode: str) -> list[dict]:
    name = op["op"]
    target = op.get("tenant") or op.get("node")
    if name == "run_rounds":
        events = []
        for _ in range(op.get("n", 1)):
            events.extend(sched.run_round())
        return events
    if name == "run_until_idle":
        return sched.run_until_idle(op.get("max_rounds", 1000))
    if name == "set_weight":
        return [sched.set_weight(target, op["weight"])]
    if name == "pause":
        return [sched.pause(target)]
    if name == "resume":
        return [sched.resume(target)]
    if name == "enqueue":
        return [sched.try_enqueue(target, op["task_id"], op["cost"])]
    if name == "migrate" and mode == "tree":
        return sched.migrate_leaf(op["leaf"], op["new_parent"])
    if name == "snapshot" and mode == "tree":
        return [{"event": "snapshot", "snapshot": sched.snapshot()}]
    raise ValueError(f"unknown or unsupported operation: {op!r}")


def run_flat(spec: dict) -> None:
    sched = FlatDRR(quantum=spec["quantum"], deficit_cap=spec["deficit_cap"])
    for t in spec.get("tenants", []):
        sched.add_tenant(t["id"], weight=t.get("weight", 1), cap=t.get("cap"))
        for task_id, cost in t.get("tasks", []):
            emit(sched.try_enqueue(t["id"], task_id, cost))
    for op in spec.get("operations", []):
        for ev in apply_op(sched, op, "flat"):
            emit(ev)


def run_tree(spec: dict) -> None:
    sched = TreeDRR()
    for nd in spec.get("nodes", []):
        sched.add_node(nd["id"], nd["parent"], nd["weight"], nd["quantum"],
                       nd["cap"], leaf=nd.get("leaf", False))
        for task_id, cost in nd.get("tasks", []):
            emit(sched.try_enqueue(nd["id"], task_id, cost))
    for op in spec.get("operations", []):
        for ev in apply_op(sched, op, "tree"):
            emit(ev)


def main(argv: list[str]) -> int:
    if len(argv) != 3 or argv[1] not in ("run", "check"):
        print(__doc__.strip(), file=sys.stderr)
        return 2
    with open(argv[2], "r", encoding="utf-8") as fh:
        spec = json.load(fh)
    if argv[1] == "check":
        report = check_starvation(
            spec, spec.get("streams", {}), spec["victim_leaf"],
            spec["victim_cost"], spec.get("state_bound", 100_000),
        )
        emit(report)
        return 0
    mode = spec.get("mode")
    if mode == "flat":
        run_flat(spec)
    elif mode == "tree":
        run_tree(spec)
    else:
        print(f"unknown mode {mode!r}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
