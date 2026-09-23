"""Independent verifier for Mealy transition-coverage policies.

Python 3.11 standard library only.  Shares nothing with the planner except
the model loader; it trusts nothing the policy claims.

Checks
------
1. Structure: the policy is a finite JSON tree of "action" / "goal" nodes.
   A finite tree terminates on every branch by construction -- the verifier
   never assumes that an infinite loop "eventually" covers anything, and
   there is no way to reference a node cyclically.
2. Simulation: for every actual initial state in ``initial_candidates``
   the induced trajectory is replayed:
   * every input must be defined at the current actual state (legality,
     which over all candidates is exactly the planner's safety condition);
   * every reset must be allowed at the current actual state;
   * the produced output must have a matching branch;
   * costs are accumulated with the model's own cost table.
3. Coverage: trajectories are grouped by the goal leaf they reach; a
   transition is definitely covered at a leaf iff *all* trajectories
   ending there executed it.  Each leaf's intersection must contain every
   transition reachable from the initial candidates.
4. Cost: the recomputed worst trajectory cost must match the policy's
   ``claimed_worst_cost``.
"""
from __future__ import annotations

import argparse
import json
import sys

from mealy import Model


class _Walk:
    def __init__(self, model: Model):
        self.m = model
        self.errors: list[str] = []
        self.warnings: list[str] = []
        self.leaf_trajs: dict[int, list[tuple[str, int, frozenset]]] = {}
        self.leaf_nodes: dict[int, dict] = {}
        self.visited: set[int] = set()
        self.all_nodes: set[int] = set()

    # -------------------------------------------------- structural pass
    def check_structure(self, node, path: str) -> None:
        self.all_nodes.add(id(node))
        if not isinstance(node, dict):
            self.errors.append(f"{path}: node must be an object")
            return
        kind = node.get("kind")
        if kind == "goal":
            if "branches" in node:
                self.errors.append(f"{path}: goal node must not have branches")
            return
        if kind != "action":
            self.errors.append(f"{path}: unknown node kind {kind!r}")
            return
        act = node.get("action")
        if not isinstance(act, dict) or act.get("type") not in ("input", "reset"):
            self.errors.append(f"{path}: action node needs action.type in "
                               f"('input','reset')")
            return
        if act["type"] == "input" and act.get("symbol") not in self.m.inputs:
            self.errors.append(f"{path}: unknown input symbol "
                               f"{act.get('symbol')!r}")
            return
        branches = node.get("branches")
        if not isinstance(branches, dict) or not branches:
            self.errors.append(f"{path}: action node needs non-empty branches")
            return
        if act["type"] == "reset" and set(branches) != {""}:
            self.errors.append(f"{path}: reset node must have exactly one "
                               f"branch keyed ''")
            return
        for out, child in branches.items():
            if act["type"] == "input" and out not in self.m.outputs:
                self.errors.append(f"{path}: branch key {out!r} is not an "
                                   f"output of the model")
            self.check_structure(child, f"{path}/{out!r}")

    # -------------------------------------------------- simulation pass
    def simulate(self, node, state: str, cost: int, cov: frozenset,
                 path: str) -> None:
        self.visited.add(id(node))
        if not isinstance(node, dict):
            return  # structural error already reported
        kind = node.get("kind")
        if kind == "goal":
            self.leaf_trajs.setdefault(id(node), []).append((state, cost, cov))
            self.leaf_nodes[id(node)] = node
            return
        if kind != "action":
            return
        act = node["action"]
        branches = node.get("branches") or {}
        if act["type"] == "input":
            sym = act["symbol"]
            nxt = self.m.next(state, sym)
            if nxt is None:
                self.errors.append(
                    f"{path}: input {sym!r} is not defined at actual state "
                    f"{state!r} (unsafe action)")
                return
            ns, out = nxt
            child = branches.get(out)
            if child is None:
                self.errors.append(
                    f"{path}: machine produced output {out!r} at state "
                    f"{state!r} on {sym!r}, but the policy has no such branch")
                return
            self.simulate(child, ns, cost + self.m.input_costs[sym],
                          cov | {(state, sym)}, f"{path}/{out}")
        else:  # reset
            if not self.m.has_reset():
                self.errors.append(f"{path}: reset used but the model has none")
                return
            if state not in self.m.reset_allowed:
                self.errors.append(
                    f"{path}: reset is not allowed at actual state "
                    f"{state!r}")
                return
            child = branches.get("")
            if child is None:
                self.errors.append(f"{path}: reset node missing '' branch")
                return
            self.simulate(child, self.m.reset_target,
                          cost + self.m.reset_cost, cov, f"{path}/<reset>")


def verify(model: Model, policy_doc: dict) -> dict:
    w = _Walk(model)
    if not isinstance(policy_doc, dict):
        return {"ok": False, "errors": ["policy document must be an object"]}
    if policy_doc.get("format") != "mealy-policy-v1":
        w.errors.append("unknown or missing policy format "
                        "(expected 'mealy-policy-v1')")
    root = policy_doc.get("root")
    if root is None:
        w.errors.append("policy document has no 'root'")
        return {"ok": False, "errors": w.errors}
    claimed = policy_doc.get("claimed_worst_cost")

    w.check_structure(root, "$")
    for s0 in model.initial_candidates:
        w.simulate(root, s0, 0, frozenset(), "$")

    unreachable = w.all_nodes - w.visited
    if unreachable:
        w.warnings.append(
            f"{len(unreachable)} policy node(s) are unreachable from the "
            f"initial candidates and were not verified")

    # coverage per leaf: intersection over all trajectories ending there
    target = model.all_transitions
    for leaf_id, trajs in w.leaf_trajs.items():
        inter = set(trajs[0][2])
        for _st, _c, cov in trajs[1:]:
            inter &= cov
        missing = target - inter
        if missing:
            w.errors.append(
                f"goal leaf reaches {len(trajs)} trajectory(ies) but lacks "
                f"definite coverage of {sorted(map(list, missing))} "
                f"(only the intersection over all possible actual "
                f"trajectories counts)")

    worst = 0
    for trajs in w.leaf_trajs.values():
        for _st, c, _cov in trajs:
            worst = max(worst, c)
    if not w.leaf_trajs:
        w.errors.append("no trajectory reaches a goal leaf")
    if claimed is not None and worst != claimed and not w.errors:
        w.errors.append(
            f"claimed worst cost {claimed} but recomputed worst trajectory "
            f"cost is {worst}")

    return {
        "ok": not w.errors,
        "errors": w.errors,
        "warnings": w.warnings,
        "worst_cost": worst,
        "goal_leaves": len(w.leaf_trajs),
        "trajectories": sum(len(t) for t in w.leaf_trajs.values()),
    }


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="Mealy policy verifier")
    ap.add_argument("model")
    ap.add_argument("policy")
    args = ap.parse_args(argv)
    model = Model.from_json_file(args.model)
    with open(args.policy, "r", encoding="utf-8") as fh:
        policy_doc = json.load(fh)
    report = verify(model, policy_doc)
    json.dump(report, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
