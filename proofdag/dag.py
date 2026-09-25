"""Proof DAG storage with cycle rejection, back-references and incremental
edge deletion with reachability-based revocation of shared subgraphs."""

from __future__ import annotations

from .model import Fact, Step


class CycleError(ValueError):
    """Raised when a proof graph contains a cycle."""


class UnknownNodeError(ValueError):
    """Raised when a premise or goal references a node that does not exist."""


class ProofDAG:
    def __init__(self) -> None:
        self.facts: dict[str, Fact] = {}
        self.steps: dict[str, Step] = {}
        self.goals: list[str] = []
        # node_id -> set of step node_ids that use it as a premise.
        self.parents: dict[str, set[str]] = {}

    def add_fact(self, fact: Fact) -> str:
        nid = fact.node_id
        existing = self.facts.get(nid)
        if existing is not None and existing != fact:
            raise ValueError(f"conflicting fact for node {nid!r}")
        self.facts[nid] = fact
        self.parents.setdefault(nid, set())
        return nid

    def add_step(self, step: Step) -> str:
        if step.node_id in self.steps:
            raise ValueError(f"duplicate step node {step.node_id!r}")
        self.steps[step.node_id] = step
        self.parents.setdefault(step.node_id, set())
        for pid in step.premises:
            # Forward references are allowed; canonicalize() validates them.
            self.parents.setdefault(pid, set()).add(step.node_id)
        return step.node_id

    def add_goal(self, node_id: str) -> None:
        if node_id not in self.goals:
            self.goals.append(node_id)

    def remove_edge(self, step_node_id: str, premise_node_id: str) -> list[str]:
        """Remove one edge step->premise, then revoke nodes that are no
        longer reachable from any goal.  Returns revoked node ids."""
        step = self.steps[step_node_id]
        if premise_node_id not in step.premises:
            raise ValueError(f"no edge {step_node_id!r} -> {premise_node_id!r}")
        premises = list(step.premises)
        premises.remove(premise_node_id)
        step.premises = tuple(premises)
        self.parents[premise_node_id].discard(step_node_id)
        return self._gc()

    def remove_step(self, node_id: str) -> list[str]:
        step = self.steps.pop(node_id)
        for pid in step.premises:
            if pid in self.parents:
                self.parents[pid].discard(node_id)
        self.parents.pop(node_id, None)
        if node_id in self.goals:
            self.goals.remove(node_id)
        return [node_id] + self._gc()

    def reachable(self) -> set[str]:
        seen: set[str] = set()
        stack = list(self.goals)
        while stack:
            nid = stack.pop()
            if nid in seen:
                continue
            seen.add(nid)
            step = self.steps.get(nid)
            if step is not None:
                stack.extend(step.premises)
        return seen

    def _gc(self) -> list[str]:
        reachable = self.reachable()
        revoked: list[str] = []
        for nid in list(self.steps):
            if nid not in reachable:
                step = self.steps.pop(nid)
                for pid in step.premises:
                    if pid in self.parents:
                        self.parents[pid].discard(nid)
                self.parents.pop(nid, None)
                revoked.append(nid)
        for nid in list(self.facts):
            if nid not in reachable:
                del self.facts[nid]
                self.parents.pop(nid, None)
                revoked.append(nid)
        return revoked
