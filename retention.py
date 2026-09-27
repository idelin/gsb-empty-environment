#!/usr/bin/env python3.11
"""Hierarchical retention-policy deletion planner.

A file's deletable time is constrained by:
  1. retention lower bounds inherited from every ancestor directory,
  2. the file's own retention lower bound, and
  3. legal-hold flags (its own or any ancestor's), which mean "never".

The earliest deletable time is the MAXIMUM of all effective lower bounds;
a legal hold is treated as an infinite bound.

Plans are snapshots tagged with per-node version numbers.  Any mutation
(move, retention change, hold change) bumps the version of the affected
subtree, so executing a stale plan entry is skipped with an explicit reason.

An earliest-deletable min-heap index lets time advance without a full scan:
only expired heap entries are popped; stale entries are discarded lazily.

Python 3.11, standard library only.  All sample data is synthetic.
"""
from __future__ import annotations

import argparse
import heapq
import json
import sys
from dataclasses import dataclass, field

INFINITE = "infinite"
_INF = float("inf")


@dataclass
class Node:
    id: int
    name: str
    is_dir: bool
    parent: "Node | None" = None
    children: dict[str, "Node"] = field(default_factory=dict)
    retention_until: float | None = None   # own absolute lower bound
    legal_hold: bool = False               # own legal-hold flag
    version: int = 0                       # bumped on every effective change
    inherited_bound: float = 0.0           # max retention_until over ancestors
    inherited_hold: bool = False           # any legal_hold over ancestors


class RetentionStore:
    """Directory tree + retention constraints + earliest-deletable index."""

    def __init__(self) -> None:
        self.root = Node(id=0, name="", is_dir=True)
        self.nodes: dict[int, Node] = {0: self.root}
        self._next_id = 1
        self._heap: list[tuple[float, int, int]] = []  # (bound, version, id)
        self.now = 0.0
        self.heap_pops = 0  # instrumentation: proves no full scan on advance

    # ---- path helpers -------------------------------------------------
    @staticmethod
    def _parts(path: str) -> list[str]:
        return [p for p in path.split("/") if p]

    def resolve(self, path: str) -> Node:
        node = self.root
        for part in self._parts(path):
            if not node.is_dir or part not in node.children:
                raise KeyError(f"path not found: {path}")
            node = node.children[part]
        return node

    def path_of(self, node: Node) -> str:
        parts: list[str] = []
        while node.parent is not None:
            parts.append(node.name)
            node = node.parent
        return "/" + "/".join(reversed(parts))

    # ---- constraint computation ---------------------------------------
    def effective_bound(self, node: Node) -> float:
        """Max of own and inherited retention lower bounds (ignores holds)."""
        return max(node.inherited_bound, node.retention_until or 0.0)

    def on_legal_hold(self, node: Node) -> bool:
        return node.legal_hold or node.inherited_hold

    def deletable_at(self, node: Node) -> float:
        """Earliest deletable time; infinity while any legal hold applies."""
        if self.on_legal_hold(node):
            return _INF
        return self.effective_bound(node)

    def _refresh(self, node: Node) -> None:
        """Recompute inherited constraints and bump versions for a subtree.

        Called after any mutation so that snapshot versions are invalidated
        and the index is updated for every affected file.
        """
        if node.parent is None:
            node.inherited_bound = 0.0
            node.inherited_hold = False
        else:
            parent = node.parent
            node.inherited_bound = max(
                parent.inherited_bound, parent.retention_until or 0.0
            )
            node.inherited_hold = parent.inherited_hold or parent.legal_hold
        node.version += 1
        if node.is_dir:
            for child in node.children.values():
                self._refresh(child)
        else:
            self._index(node)

    def _index(self, node: Node) -> None:
        bound = self.deletable_at(node)
        if bound != _INF:
            heapq.heappush(self._heap, (bound, node.version, node.id))

    # ---- mutations ------------------------------------------------------
    def _add(self, path: str, is_dir: bool, retention_until, legal_hold) -> Node:
        parts = self._parts(path)
        if not parts:
            raise ValueError("cannot create root")
        parent = self.root
        for part in parts[:-1]:
            if part not in parent.children or not parent.children[part].is_dir:
                raise KeyError(f"parent directory missing for: {path}")
            parent = parent.children[part]
        name = parts[-1]
        if name in parent.children:
            raise ValueError(f"already exists: {path}")
        node = Node(
            id=self._next_id, name=name, is_dir=is_dir, parent=parent,
            retention_until=retention_until, legal_hold=legal_hold,
        )
        self._next_id += 1
        parent.children[name] = node
        self.nodes[node.id] = node
        self._refresh(node)
        return node

    def mkdir(self, path, retention_until=None, legal_hold=False) -> Node:
        return self._add(path, True, retention_until, legal_hold)

    def create(self, path, retention_until=None, legal_hold=False) -> Node:
        return self._add(path, False, retention_until, legal_hold)

    def move(self, src_path: str, dst_dir_path: str) -> Node:
        node = self.resolve(src_path)
        dst = self.resolve(dst_dir_path)
        if node is self.root:
            raise ValueError("cannot move root")
        if not dst.is_dir:
            raise ValueError(f"not a directory: {dst_dir_path}")
        cursor: Node | None = dst
        while cursor is not None:
            if cursor is node:
                raise ValueError("cannot move a directory into its own subtree")
            cursor = cursor.parent
        if node.name in dst.children:
            raise ValueError(f"name already exists in {dst_dir_path}: {node.name}")
        del node.parent.children[node.name]  # type: ignore[union-attr]
        node.parent = dst
        dst.children[node.name] = node
        self._refresh(node)  # recompute inherited constraints after move
        return node

    def set_retention(self, path: str, retention_until) -> Node:
        node = self.resolve(path)
        node.retention_until = retention_until
        self._refresh(node)
        return node

    def set_hold(self, path: str, on: bool) -> Node:
        node = self.resolve(path)
        node.legal_hold = on
        self._refresh(node)
        return node

    def delete(self, path: str) -> None:
        node = self.resolve(path)
        if node is self.root:
            raise ValueError("cannot delete root")
        self._detach(node)

    def _detach(self, node: Node) -> None:
        if node.parent is not None:
            del node.parent.children[node.name]
            node.parent = None
        stack = [node]
        while stack:
            current = stack.pop()
            self.nodes.pop(current.id, None)
            stack.extend(current.children.values())

    # ---- time / index ---------------------------------------------------
    def advance(self, now: float) -> None:
        """Advance the clock.  O(1): no scan happens here."""
        if now < self.now:
            raise ValueError("time cannot move backwards")
        self.now = now

    def due(self, now: float | None = None) -> list[dict]:
        """Files deletable at `now`, popped from the heap index only."""
        moment = self.now if now is None else now
        ready: list[dict] = []
        while self._heap and self._heap[0][0] <= moment:
            bound, version, node_id = heapq.heappop(self._heap)
            self.heap_pops += 1
            node = self.nodes.get(node_id)
            if node is None or node.version != version:
                continue  # stale index entry: mutated or deleted since push
            if self.deletable_at(node) != bound:
                continue  # defensive; version check should already cover this
            ready.append({"id": node_id, "path": self.path_of(node),
                          "earliest": bound})
        return ready

    # ---- plan / execute ---------------------------------------------------
    def plan(self) -> dict:
        """Snapshot every file with its version and earliest deletable time."""
        entries = []
        for node in sorted(self.nodes.values(), key=lambda n: n.id):
            if node.is_dir:
                continue
            bound = self.deletable_at(node)
            entries.append({
                "id": node.id,
                "path": self.path_of(node),
                "version": node.version,
                "earliest": INFINITE if bound == _INF else bound,
            })
        return {"now": self.now, "entries": entries}

    def execute(self, plan: dict, now: float | None = None) -> list[dict]:
        """Execute a plan snapshot; skip entries whose version changed."""
        moment = self.now if now is None else now
        results = []
        for entry in plan["entries"]:
            record = {"id": entry["id"], "path": entry["path"]}
            node = self.nodes.get(entry["id"])
            if node is None:
                record.update(status="skipped",
                              reason="node no longer exists")
            elif node.version != entry["version"]:
                record.update(
                    status="skipped",
                    reason=(f"version changed: snapshot v{entry['version']}, "
                            f"current v{node.version}"),
                )
            else:
                bound = self.deletable_at(node)
                if bound == _INF:
                    record.update(status="skipped",
                                  reason="legal hold in effect")
                elif bound > moment:
                    record.update(
                        status="skipped",
                        reason=f"not yet due: earliest {bound} > now {moment}",
                    )
                else:
                    record.update(status="deleted", earliest=bound)
                    record["path"] = self.path_of(node)
                    self._detach(node)
            results.append(record)
        return results


# ---- scenario-driven CLI ----------------------------------------------------
def run_scenario(ops: list[dict]) -> list[dict]:
    store = RetentionStore()
    plans: dict[str, dict] = {}
    results: list[dict] = []
    for op in ops:
        kind = op["op"]
        if kind == "mkdir":
            node = store.mkdir(op["path"], op.get("retention_until"),
                               op.get("legal_hold", False))
            results.append({"op": kind, "path": op["path"], "id": node.id})
        elif kind == "create":
            node = store.create(op["path"], op.get("retention_until"),
                                op.get("legal_hold", False))
            results.append({"op": kind, "path": op["path"], "id": node.id})
        elif kind == "move":
            node = store.move(op["src"], op["dst_dir"])
            results.append({"op": kind, "path": store.path_of(node),
                            "version": node.version})
        elif kind == "set_retention":
            node = store.set_retention(op["path"], op.get("retention_until"))
            results.append({"op": kind, "path": op["path"],
                            "version": node.version})
        elif kind == "set_hold":
            node = store.set_hold(op["path"], bool(op["on"]))
            results.append({"op": kind, "path": op["path"],
                            "version": node.version})
        elif kind == "delete":
            store.delete(op["path"])
            results.append({"op": kind, "path": op["path"], "deleted": True})
        elif kind == "tick":
            store.advance(op["now"])
            results.append({"op": kind, "now": store.now})
        elif kind == "due":
            results.append({"op": kind, "now": store.now, "due": store.due()})
        elif kind == "plan":
            snapshot = store.plan()
            plans[op.get("id", f"plan{len(plans)}")] = snapshot
            results.append({"op": kind, "plan": snapshot})
        elif kind == "execute":
            plan_id = op["plan"]
            if plan_id not in plans:
                raise KeyError(f"unknown plan id: {plan_id}")
            results.append({"op": kind, "plan": plan_id, "now": store.now,
                            "results": store.execute(plans[plan_id])})
        else:
            raise ValueError(f"unknown op: {kind}")
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Hierarchical retention-policy deletion planner.")
    sub = parser.add_subparsers(dest="command", required=True)
    run = sub.add_parser("run", help="run a scenario JSON file ('-' for stdin)")
    run.add_argument("scenario", help="path to scenario JSON, or '-' for stdin")
    args = parser.parse_args(argv)

    if args.command == "run":
        text = sys.stdin.read() if args.scenario == "-" else \
            open(args.scenario, encoding="utf-8").read()
        ops = json.loads(text)["ops"]
        output = {"results": run_scenario(ops)}
        json.dump(output, sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
