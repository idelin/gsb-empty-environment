"""Exact AND/OR planner for definite transition coverage of Mealy machines.

Python 3.11 standard library only.  No automata/planning libraries.

Belief-state semantics
----------------------
The tester only sees outputs, so a *belief node* must capture every actual
trajectory still consistent with the observation history.  Because the
machine is deterministic and the policy is a function of the observations,
each initial candidate state induces exactly one actual trajectory per
output branch.  A node is therefore a set of *worlds*::

    world = (current_state, frozenset of transitions this trajectory executed)

Two trajectories that agree on both components are merged (state-space
reduction R1).

A transition t is *definitely covered* at a node iff it lies in the
intersection of all world coverages, i.e. every possible actual trajectory
has executed it.  Edges that different hidden states merely *might* have
covered are never merged into "covered".

Goal: every world of the node covers ``model.all_transitions`` (equivalent
to: the intersection covers everything).

Search
------
Exact AND/OR search minimizing the worst root-to-leaf branch cost:
* OR choice  : a safe action (input defined on all candidates, or reset
               allowed on all candidates);
* AND split  : one child per output the action can produce here.

A node repeated on its own branch means zero progress (coverage is part of
the node), so such branches are cut (reduction R4) -- any policy containing
such a loop can be strictly shortened, hence the cut preserves completeness
and optimality.  Consequently every emitted policy is a finite tree and
termination needs no liveness assumption.

Budget
------
``budget`` bounds the number of expanded nodes.  On exhaustion the planner
returns status "unknown" with a valid lower bound (admissible heuristic
propagated through min/max) and the cost of the best complete policy found
so far (upper bound, possibly infinite).  "No policy found within budget"
is never reported as "unsolvable".
"""
from __future__ import annotations

import argparse
import json
import math
import sys
from dataclasses import dataclass, field

from mealy import Model

INF = math.inf

# A world: (state, frozenset of covered (state, input) transitions)
World = tuple
# A belief node: frozenset of worlds
Node = frozenset


@dataclass
class Result:
    status: str            # "exact" | "cutoff" | "fail"
    lb: float              # admissible lower bound on optimal cost
    ub: float              # cost of best known complete policy (INF if none)
    policy: dict | None    # policy tree (only when status == "exact")
    context_free: bool = True  # False if a "fail" depends on the path


class Planner:
    def __init__(self, model: Model, budget: int = 100_000):
        self.m = model
        self.budget = budget
        self.expanded = 0
        self.memo: dict[Node, Result] = {}
        # explanation collectors
        self.dead_ends: list[dict] = []
        self.loops: list[dict] = []
        self.precheck_problems: list[dict] = self._precheck()

    # --------------------------------------------------------- node basics
    def initial_node(self) -> Node:
        return frozenset((s, frozenset()) for s in self.m.initial_candidates)

    def is_goal(self, node: Node) -> bool:
        target = self.m.all_transitions
        return all(target <= cov for _s, cov in node)

    def definite_covered(self, node: Node) -> frozenset:
        """Transitions executed by *every* possible actual trajectory."""
        it = iter(node)
        inter = set(next(it)[1])
        for _s, cov in it:
            inter &= cov
        return frozenset(inter)

    def safe_actions(self, node: Node) -> list[tuple[str, str | None]]:
        states = {s for s, _c in node}
        actions: list[tuple[str, str | None]] = []
        for sym in self.m.inputs:
            if all((s, sym) in self.m.trans for s in states):
                actions.append(("input", sym))
        if self.m.has_reset() and states <= self.m.reset_allowed:
            actions.append(("reset", None))
        return actions

    def apply(self, node: Node, action) -> tuple[dict[str, Node], int]:
        """Successor nodes per output branch, and the action cost."""
        kind, sym = action
        if kind == "reset":
            child = frozenset((self.m.reset_target, cov) for _s, cov in node)
            return {"": child}, self.m.reset_cost
        branches: dict[str, set] = {}
        for s, cov in node:
            ns, out = self.m.trans[(s, sym)]
            branches.setdefault(out, set()).add((ns, cov | {(s, sym)}))
        return {o: frozenset(ws) for o, ws in branches.items()}, \
            self.m.input_costs[sym]

    def heuristic(self, node: Node) -> int:
        """Admissible lower bound: every trajectory must still execute each
        of its missing transitions at least once; each input application
        covers at most one transition per trajectory and costs at least the
        minimum input cost."""
        need = max(len(self.m.all_transitions - cov) for _s, cov in node)
        return need * self.m.min_input_cost

    # ------------------------------------------------------------- search
    def search(self, node: Node, path: frozenset) -> Result:
        if self.is_goal(node):
            return Result("exact", 0, 0, {"kind": "goal"})
        cached = self.memo.get(node)
        if cached is not None:
            return cached
        if node in path:
            self._record_loop(node)
            return Result("fail", INF, INF, None, context_free=False)
        if self.expanded >= self.budget:
            return Result("cutoff", self.heuristic(node), INF, None)
        self.expanded += 1

        actions = self.safe_actions(node)
        if not actions:
            self._record_dead_end(node)
            res = Result("fail", INF, INF, None)
            self.memo[node] = res
            return res

        outcomes = []  # (status, lb, ub, policy, context_free, action)
        for act in actions:
            branches, acost = self.apply(node, act)
            # R5: skip actions whose only effect is the identical node
            if len(branches) == 1 and next(iter(branches.values())) == node:
                continue
            kids = [self.search(c, path | {node}) for c in branches.values()]
            fails = [k for k in kids if k.status == "fail"]
            if fails:
                outcomes.append(("fail", INF, INF, None,
                                 all(k.context_free for k in fails), act))
                continue
            lb = acost + max(k.lb for k in kids)
            ub = INF if any(k.ub == INF for k in kids) \
                else acost + max(k.ub for k in kids)
            if all(k.status == "exact" for k in kids):
                pol = {
                    "kind": "action",
                    "action": ({"type": "input", "symbol": act[1]}
                               if act[0] == "input" else {"type": "reset"}),
                    "branches": {o: k.policy
                                 for o, k in zip(branches, kids)},
                }
                outcomes.append(("exact", lb, ub, pol, True, act))
            else:
                outcomes.append(("cutoff", lb, ub, None, True, act))

        if not outcomes:  # every action was a no-progress self-loop
            self._record_loop(node)
            res = Result("fail", INF, INF, None)
            self.memo[node] = res
            return res

        exacts = [o for o in outcomes if o[0] == "exact"]
        nonfail = [o for o in outcomes if o[0] != "fail"]
        if exacts:
            best = min(exacts, key=lambda o: o[2])
            # optimal only if no other action could be strictly cheaper
            if all(o[1] >= best[2] for o in nonfail):
                res = Result("exact", best[2], best[2], best[3])
                self.memo[node] = res
                return res
        if not nonfail:
            cf = all(o[4] for o in outcomes)
            res = Result("fail", INF, INF, None, context_free=cf)
            if cf:
                self.memo[node] = res
            return res
        lb = min(o[1] for o in nonfail)
        ub = min(o[2] for o in nonfail)
        return Result("cutoff", lb, ub, None)

    # ---------------------------------------------------------- top level
    def plan(self) -> dict:
        if self.precheck_problems:
            return {
                "status": "unsolvable",
                "reason": "some reachable transition can never be executed "
                          "by every possible actual trajectory",
                "explanation": {"precheck": self.precheck_problems},
                "stats": self._stats(),
            }
        root = self.initial_node()
        res = self.search(root, frozenset())
        if res.status == "exact":
            return {
                "status": "optimal",
                "cost": res.ub,
                "policy": self._policy_doc(res.policy, res.ub),
                "stats": self._stats(),
            }
        if res.status == "fail":
            return {
                "status": "unsolvable",
                "reason": "no adaptive policy can definitely cover all "
                          "reachable transitions on every output branch",
                "explanation": {
                    "precheck": self.precheck_problems,
                    "dead_ends": self.dead_ends[:20],
                    "loops_without_progress": self.loops[:20],
                },
                "stats": self._stats(),
            }
        out = {
            "status": "unknown",
            "reason": "search budget exhausted; this is NOT a proof of "
                      "unsolvability",
            "lower_bound": res.lb,
            "upper_bound": (res.ub if res.ub != INF else None),
            "stats": self._stats(),
        }
        return out

    def _policy_doc(self, root_policy: dict, cost: float) -> dict:
        return {
            "format": "mealy-policy-v1",
            "model_name": self.m.name,
            "claimed_worst_cost": cost,
            "root": root_policy,
        }

    def _stats(self) -> dict:
        return {"expanded": self.expanded, "memoized": len(self.memo),
                "budget": self.budget}

    # -------------------------------------------------------- explanations
    def _precheck(self) -> list[dict]:
        """Transitions that some initial candidate's trajectory can never
        execute (its source state is unreachable from that candidate, even
        using resets).  Such instances are unsolvable regardless of search."""
        m = self.m
        adj: dict[str, set[str]] = {s: set() for s in m.states}
        for (s, _i), (ns, _o) in m.trans.items():
            adj[s].add(ns)
        if m.reset_target is not None:
            for s in m.reset_allowed:
                adj[s].add(m.reset_target)
        problems = []
        for s0 in m.initial_candidates:
            seen = {s0}
            stack = [s0]
            while stack:
                x = stack.pop()
                for y in adj[x]:
                    if y not in seen:
                        seen.add(y)
                        stack.append(y)
            for (s, i) in sorted(m.all_transitions):
                if s not in seen:
                    problems.append({
                        "initial_state": s0,
                        "transition": [s, i],
                        "reason": f"state {s!r} is unreachable from initial "
                                  f"candidate {s0!r}; its trajectory can "
                                  f"never execute ({s},{i})",
                    })
        return problems

    def _record_dead_end(self, node: Node) -> None:
        states = sorted({s for s, _c in node})
        undefined = {
            i: [s for s in states if (s, i) not in self.m.trans]
            for i in self.m.inputs
        }
        entry = {
            "candidates": states,
            "inputs_not_safe": {i: ss for i, ss in undefined.items() if ss},
            "reset_blocked_by": (
                [s for s in states if s not in self.m.reset_allowed]
                if self.m.has_reset() else "reset not available in model"),
            "uncovered_definite": sorted(
                map(list, self.m.all_transitions - self.definite_covered(node))),
        }
        if entry not in self.dead_ends:
            self.dead_ends.append(entry)

    def _record_loop(self, node: Node) -> None:
        entry = {
            "candidates": sorted({s for s, _c in node}),
            "uncovered_definite": sorted(
                map(list, self.m.all_transitions - self.definite_covered(node))),
            "note": "belief revisited with no new definite coverage; every "
                    "continuation loops without progress and cannot return "
                    "to the uncovered transitions",
        }
        if entry not in self.loops:
            self.loops.append(entry)


def policy_depth(node: dict) -> int:
    if node["kind"] == "goal":
        return 0
    return 1 + max(policy_depth(c) for c in node["branches"].values())


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="Mealy transition-coverage planner")
    ap.add_argument("model")
    ap.add_argument("--budget", type=int, default=100_000)
    ap.add_argument("--policy-out", default=None)
    args = ap.parse_args(argv)

    model = Model.from_json_file(args.model)
    planner = Planner(model, budget=args.budget)
    result = planner.plan()
    if args.policy_out and result.get("policy"):
        with open(args.policy_out, "w", encoding="utf-8") as fh:
            json.dump(result["policy"], fh, indent=2)
        result = dict(result)
        result["policy"] = f"<written to {args.policy_out}>"
    json.dump(result, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
