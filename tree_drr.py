"""Hierarchical (tenant-tree) deficit round-robin scheduler.

Python 3.11, standard library only. All example data is synthetic.

Structure: a root scheduler node with arbitrary internal nodes and leaf
tenants (the typical deployment is the two-level tenant tree
root -> group -> leaf). Every non-root node has its own weight, quantum
and deficit cap. Leaves hold FIFO task queues with integer costs.

Path semantics:
  * A leaf task of cost C may dequeue only if EVERY node on the path from
    the root (exclusive) to the leaf (inclusive) has deficit >= C.
  * On dequeue, C is deducted from EVERY node on that path simultaneously.
  * A task whose cost exceeds the minimum cap along its path is infeasible
    and is rejected explicitly at enqueue time.

Round-advancement rules (avoid child idle-spin draining parent quantum):
  1. A node is credited ``weight * quantum`` at most once per root round,
     and only when its subtree has eligible (unpaused) backlog.
  2. An idle node (no eligible backlog in its subtree) has its deficit
     reset to 0: credit is never banked by empty subtrees, so idle
     children cannot accumulate service at the parent's expense.
  3. A paused node freezes its whole subtree: no credit, no service, and
     its deficit is preserved (not reset) until resumed.

Migration: moving a leaf to a new parent truncates the leaf deficit to
the minimum cap along the NEW path (never increases it -- no service is
minted out of thin air), and tasks that are infeasible under the new
path caps are dropped with explicit evidence.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass


@dataclass(frozen=True)
class Task:
    task_id: str
    cost: int


class InfeasibleTaskError(ValueError):
    """Raised when a task can never be served along its current path."""


class Node:
    __slots__ = (
        "node_id", "weight", "quantum", "cap", "paused", "deficit",
        "cursor", "parent", "children", "queue",
    )

    def __init__(self, node_id: str, weight: int, quantum: int, cap: int | None, leaf: bool):
        self.node_id = node_id
        self.weight = weight
        self.quantum = quantum
        self.cap = cap  # None only for the root (unconstrained)
        self.paused = False
        self.deficit = 0
        self.cursor = 0
        self.parent: Node | None = None
        self.children: list[Node] = []
        self.queue: deque[Task] | None = deque() if leaf else None

    @property
    def is_leaf(self) -> bool:
        return self.queue is not None


class TreeDRR:
    ROOT = "root"

    def __init__(self):
        self.nodes: dict[str, Node] = {}
        self.root = Node(self.ROOT, weight=1, quantum=0, cap=None, leaf=False)
        self.nodes[self.ROOT] = self.root
        self.round = 0

    # ------------------------------------------------------------------ setup
    def add_node(self, node_id: str, parent: str, weight: int, quantum: int,
                 cap: int, leaf: bool = False) -> Node:
        if node_id in self.nodes:
            raise ValueError(f"duplicate node {node_id!r}")
        if weight <= 0 or quantum <= 0 or cap <= 0:
            raise ValueError("weight, quantum and cap must be positive integers")
        p = self.nodes[parent]
        if p.is_leaf:
            raise ValueError(f"parent {parent!r} is a leaf")
        node = Node(node_id, weight, quantum, cap, leaf)
        node.parent = p
        p.children.append(node)
        self.nodes[node_id] = node
        return node

    # ------------------------------------------------------------------ paths
    def path_to(self, node: Node) -> list[Node]:
        """Non-root nodes from the highest ancestor down to ``node``."""
        path = []
        nd = node
        while nd is not self.root:
            path.append(nd)
            nd = nd.parent
        path.reverse()
        return path

    def path_cap(self, node: Node) -> int:
        return min(nd.cap for nd in self.path_to(node))

    # ------------------------------------------------------------------ intake
    def try_enqueue(self, leaf: str, task_id: str, cost: int) -> dict:
        node = self.nodes[leaf]
        if not node.is_leaf:
            raise ValueError(f"{leaf!r} is not a leaf tenant")
        if cost <= 0:
            raise ValueError("cost must be a positive integer")
        allowed = self.path_cap(node)
        if cost > allowed:
            return {
                "event": "reject", "leaf": leaf, "task": task_id, "cost": cost,
                "reason": "cost_exceeds_path_cap", "path_cap": allowed,
            }
        node.queue.append(Task(task_id, cost))
        return {
            "event": "enqueue", "leaf": leaf, "task": task_id, "cost": cost,
            "queue_depth": len(node.queue),
        }

    def enqueue(self, leaf: str, task_id: str, cost: int) -> dict:
        ev = self.try_enqueue(leaf, task_id, cost)
        if ev["event"] == "reject":
            raise InfeasibleTaskError(
                f"task {task_id!r} cost {cost} exceeds path cap {ev['path_cap']} "
                f"at leaf {leaf!r}; task can never be served"
            )
        return ev

    # ------------------------------------------------------------- operations
    def set_weight(self, node_id: str, weight: int) -> dict:
        if weight <= 0:
            raise ValueError("weight must be a positive integer")
        nd = self.nodes[node_id]
        old = nd.weight
        nd.weight = weight
        return {"event": "set_weight", "node": node_id, "old_weight": old, "weight": weight}

    def pause(self, node_id: str) -> dict:
        self.nodes[node_id].paused = True
        return {"event": "pause", "node": node_id}

    def resume(self, node_id: str) -> dict:
        self.nodes[node_id].paused = False
        return {"event": "resume", "node": node_id}

    # ------------------------------------------------------------- scheduling
    def _backlogged(self, node: Node) -> bool:
        if node.paused:
            return False
        if node.is_leaf:
            return bool(node.queue)
        return any(self._backlogged(c) for c in node.children)

    def has_work(self) -> bool:
        return self._backlogged(self.root)

    def run_round(self) -> list[dict]:
        self.round += 1
        events: list[dict] = []
        self._visit_children(self.root, events)
        return events

    def _visit_children(self, node: Node, events: list[dict]) -> None:
        kids = node.children
        n = len(kids)
        if n == 0:
            return
        start = node.cursor % n
        last_idx: int | None = None
        for i in range(n):
            idx = (start + i) % n
            child = kids[idx]
            if child.paused:
                events.append({
                    "event": "skip", "round": self.round, "node": child.node_id,
                    "reason": "paused",
                })
                continue
            if not self._backlogged(child):
                if child.deficit:
                    events.append({
                        "event": "reset", "round": self.round, "node": child.node_id,
                        "deficit_before": child.deficit, "reason": "idle",
                    })
                    child.deficit = 0
                continue
            before = child.deficit
            credit = child.weight * child.quantum
            child.deficit = min(before + credit, child.cap)
            events.append({
                "event": "credit", "round": self.round, "node": child.node_id,
                "weight": child.weight, "quantum": child.quantum, "credit": credit,
                "deficit_before": before, "deficit_after": child.deficit,
                "cap": child.cap,
            })
            if child.is_leaf:
                self._serve_leaf(child, events)
            else:
                self._visit_children(child, events)
            last_idx = idx
        if last_idx is not None:
            node.cursor = (last_idx + 1) % n

    def _serve_leaf(self, leaf: Node, events: list[dict]) -> None:
        path = self.path_to(leaf)
        while leaf.queue:
            task = leaf.queue[0]
            blocker = next((nd for nd in path if nd.deficit < task.cost), None)
            if blocker is not None:
                events.append({
                    "event": "head_blocked", "round": self.round, "leaf": leaf.node_id,
                    "head_task": task.task_id, "head_cost": task.cost,
                    "blocker": blocker.node_id, "blocker_deficit": blocker.deficit,
                    "path_deficits": {nd.node_id: nd.deficit for nd in path},
                })
                break
            before = {nd.node_id: nd.deficit for nd in path}
            for nd in path:
                nd.deficit -= task.cost
            leaf.queue.popleft()
            after = {nd.node_id: nd.deficit for nd in path}
            events.append({
                "event": "dequeue", "round": self.round, "leaf": leaf.node_id,
                "task": task.task_id, "cost": task.cost,
                "path": [nd.node_id for nd in path],
                "deficit_before": before, "deficit_after": after,
            })

    def run_until_idle(self, max_rounds: int = 1000) -> list[dict]:
        events: list[dict] = []
        rounds = 0
        while self.has_work() and rounds < max_rounds:
            events.extend(self.run_round())
            rounds += 1
        if self.has_work():
            events.append({
                "event": "incomplete", "reason": "max_rounds_reached", "max_rounds": max_rounds,
            })
        return events

    # --------------------------------------------------------------- snapshot
    def snapshot(self) -> dict:
        """JSON-serializable snapshot of the full scheduler state."""
        nodes = []

        def rec(nd: Node) -> None:
            nodes.append({
                "id": nd.node_id,
                "parent": nd.parent.node_id if nd.parent else None,
                "weight": nd.weight,
                "quantum": nd.quantum,
                "cap": nd.cap,
                "paused": nd.paused,
                "deficit": nd.deficit,
                "cursor": nd.cursor,
                "leaf": nd.is_leaf,
                "children": [c.node_id for c in nd.children],
                "queue": [[t.task_id, t.cost] for t in nd.queue] if nd.is_leaf else None,
            })
            for c in nd.children:
                rec(c)

        rec(self.root)
        return {"round": self.round, "nodes": nodes}

    @classmethod
    def restore(cls, snap: dict) -> "TreeDRR":
        """Rebuild a scheduler from ``snapshot()``; restores exactly."""
        tree = cls()
        tree.round = snap["round"]
        by_id: dict[str, Node] = {}
        for nd in snap["nodes"]:
            if nd["parent"] is None:
                node = tree.root
            else:
                node = Node(nd["id"], nd["weight"], nd["quantum"], nd["cap"], nd["leaf"])
                tree.nodes[nd["id"]] = node
            node.weight = nd["weight"]
            node.quantum = nd["quantum"]
            node.cap = nd["cap"]
            node.paused = nd["paused"]
            node.deficit = nd["deficit"]
            node.cursor = nd["cursor"]
            if node.is_leaf:
                node.queue = deque(Task(t, c) for t, c in nd["queue"])
            by_id[nd["id"]] = node
        for nd in snap["nodes"]:
            node = by_id[nd["id"]]
            node.children = [by_id[c] for c in nd["children"]]
            for c in node.children:
                c.parent = node
        return tree

    # --------------------------------------------------------------- migration
    def migrate_leaf(self, leaf_id: str, new_parent_id: str) -> list[dict]:
        """Move a leaf tenant under a new parent.

        The leaf deficit is truncated to the minimum cap along the NEW
        path; it is never increased (no service is minted). Tasks whose
        cost exceeds the new path cap are dropped with evidence.
        """
        leaf = self.nodes[leaf_id]
        new_parent = self.nodes[new_parent_id]
        if not leaf.is_leaf:
            raise ValueError(f"{leaf_id!r} is not a leaf tenant")
        if new_parent.is_leaf:
            raise ValueError(f"{new_parent_id!r} is a leaf; cannot parent a node")
        old_parent = leaf.parent
        old_parent.children.remove(leaf)
        old_parent.cursor = old_parent.cursor % len(old_parent.children) if old_parent.children else 0
        new_parent.children.append(leaf)
        leaf.parent = new_parent

        allowed = self.path_cap(leaf)
        before = leaf.deficit
        if leaf.deficit > allowed:
            leaf.deficit = allowed
        kept: list[Task] = []
        dropped: list[Task] = []
        for t in leaf.queue:
            (kept if t.cost <= allowed else dropped).append(t)
        leaf.queue = deque(kept)
        return [{
            "event": "migrate", "leaf": leaf_id,
            "from": old_parent.node_id, "to": new_parent_id,
            "deficit_before": before, "deficit_after": leaf.deficit,
            "path_cap": allowed,
            "dropped_tasks": [{"task": t.task_id, "cost": t.cost} for t in dropped],
        }]
