"""Hierarchical (tree) Deficit Round Robin scheduler.

A tree of nodes; internal nodes are tenant groups, leaves are tenants with
FIFO task queues.  Every node has weight, quantum and a deficit cap.

Round-progression rules (deterministic, anti-drain):
  R1. One global round = one DFS traversal of the tree in fixed child order.
  R2. A node is credited AT MOST ONCE per round, when first visited, and only
      if it is *active*: not effectively paused and having at least one
      pending task in its subtree.  This prevents child-level spinning from
      draining the parent's quantum: no matter how many leaves are visited,
      the parent receives exactly one credit per round, and idle subtrees
      receive none.
  R3. Effective pause: a node paused directly or via any ancestor is skipped
      together with its whole subtree (no credit, no service).
  R4. A leaf's head task is served iff its cost <= deficit of EVERY node on
      the root-to-leaf path; on service the cost is deducted from ALL nodes
      on the path simultaneously (atomic multi-level debit).
  R5. A head task whose cost exceeds the minimum cap along its path can never
      be served: it is explicitly rejected (removed, with evidence).

Migration:
  * A leaf may be moved to another parent between rounds.
  * Its accumulated deficit is truncated to the minimum cap along the NEW
    path (and its own cap).  Truncation only ever decreases the deficit, so
    migration can never mint service that was not earned ("no free service").

Snapshots: to_snapshot()/from_snapshot() capture the full state (deficits,
queues, pause flags, tree structure, round number) as plain JSON data.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Any, Iterator


@dataclass
class HTask:
    cost: int
    tag: str = ""

    def to_dict(self) -> dict:
        return {"tag": self.tag, "cost": self.cost}


@dataclass
class Node:
    nid: str
    weight: int
    quantum: int
    cap: int
    parent: "Node | None" = field(default=None, repr=False)
    children: list["Node"] = field(default_factory=list, repr=False)
    tasks: deque = field(default_factory=deque, repr=False)  # leaves only
    paused: bool = False
    deficit: int = 0
    rejected: list = field(default_factory=list, repr=False)
    stream: tuple = ()      # cyclic task-cost stream (infinite source), leaves only
    stream_pos: int = 0     # next stream index to materialise

    @property
    def is_leaf(self) -> bool:
        return not self.children

    def path(self) -> list["Node"]:
        """Root-to-self path."""
        node, out = self, []
        while node is not None:
            out.append(node)
            node = node.parent
        return list(reversed(out))

    def min_path_cap(self) -> int:
        return min(n.cap for n in self.path())

    def effectively_paused(self) -> bool:
        return any(n.paused for n in self.path())

    def has_pending_work(self) -> bool:
        if self.is_leaf:
            return len(self.tasks) > 0 or bool(self.stream)
        return any(c.has_pending_work() for c in self.children)


class HierDRR:
    def __init__(self, root: Node):
        self.root = root
        self.round_no = 0
        self.trace: list[dict] = []
        self._index: dict[str, Node] = {}
        for n in self._walk(root):
            if n.nid in self._index:
                raise ValueError(f"duplicate node id: {n.nid}")
            self._index[n.nid] = n

    # ---- traversal helpers ----
    @staticmethod
    def _walk(node: Node) -> Iterator[Node]:
        yield node
        for c in node.children:
            yield from HierDRR._walk(c)

    def node(self, nid: str) -> Node:
        return self._index[nid]

    def leaves(self) -> list[Node]:
        return [n for n in self._walk(self.root) if n.is_leaf]

    # ---- dynamic operations ----
    def set_weight(self, nid: str, weight: int) -> None:
        if weight < 0:
            raise ValueError("weight must be >= 0")
        self.node(nid).weight = weight

    def pause(self, nid: str) -> None:
        self.node(nid).paused = True

    def resume(self, nid: str) -> None:
        self.node(nid).paused = False

    def migrate(self, leaf_id: str, new_parent_id: str) -> dict:
        leaf = self.node(leaf_id)
        new_parent = self.node(new_parent_id)
        if not leaf.is_leaf:
            raise ValueError("only leaf tenants can migrate")
        if new_parent.tasks or new_parent.stream:
            raise ValueError("migration target must be an internal node (no task queue)")
        if leaf.parent is None:
            raise ValueError("cannot migrate the root")
        old_path = [n.nid for n in leaf.path()]
        leaf.parent.children.remove(leaf)
        leaf.parent = new_parent
        new_parent.children.append(leaf)
        # Truncate accumulated deficit by the NEW path's caps; never increase.
        before = leaf.deficit
        leaf.deficit = min(leaf.deficit, leaf.min_path_cap())
        new_path = [n.nid for n in leaf.path()]
        ev = {"op": "migrate", "leaf": leaf_id, "from_path": old_path,
              "to_path": new_path, "deficit_before": before,
              "deficit_after": leaf.deficit,
              "rule": "deficit truncated to min cap along new path; never increased"}
        return ev

    # ---- core round ----
    def run_round(self) -> dict:
        self.round_no += 1
        credited: set[int] = set()   # id(node) -> credited at most once per round (R2)
        record: dict[str, Any] = {"round": self.round_no, "credits": [], "dequeues": [],
                                  "rejections": [], "skipped": []}

        def credit(n: Node) -> None:
            if id(n) in credited:
                return
            credited.add(id(n))
            before = n.deficit
            amt = n.weight * n.quantum
            n.deficit = min(n.deficit + amt, n.cap)
            record["credits"].append({"node": n.nid, "deficit_before": before,
                                      "credit": amt, "deficit_after": n.deficit, "cap": n.cap})

        def visit(node: Node) -> None:
            if node.paused:
                record["skipped"].append({"node": node.nid, "reason": "paused (subtree skipped)"})
                return
            if not node.has_pending_work():
                record["skipped"].append({"node": node.nid, "reason": "no pending work"})
                return
            credit(node)
            if node.is_leaf:
                self._serve_leaf(node, record)
            else:
                for c in list(node.children):
                    visit(c)

        visit(self.root)
        record["deficits"] = {n.nid: n.deficit for n in self._walk(self.root)}
        self.trace.append(record)
        return record

    def _refill(self, leaf: Node) -> None:
        if leaf.stream and not leaf.tasks:
            cost = leaf.stream[leaf.stream_pos % len(leaf.stream)]
            leaf.tasks.append(HTask(cost=cost, tag=f"{leaf.nid}@{leaf.stream_pos}"))
            leaf.stream_pos += 1

    def _serve_leaf(self, leaf: Node, record: dict) -> None:
        path = leaf.path()
        while True:
            self._refill(leaf)
            if not leaf.tasks:
                break
            head = leaf.tasks[0]
            min_cap = min(n.cap for n in path)
            if head.cost > min_cap:
                leaf.tasks.popleft()
                rej = {"leaf": leaf.nid, "task": head.tag, "cost": head.cost,
                       "rejected": True,
                       "reason": f"cost {head.cost} > min path cap {min_cap}: permanently infeasible",
                       "path": [n.nid for n in path]}
                leaf.rejected.append(rej)
                record["rejections"].append(rej)
                continue
            blocker = next((n for n in path if n.deficit < head.cost), None)
            if blocker is not None:
                record["dequeues"].append({
                    "leaf": leaf.nid, "task": head.tag, "cost": head.cost,
                    "blocked": True, "blocker": blocker.nid,
                    "path_deficits": {n.nid: n.deficit for n in path},
                    "reason": f"node {blocker.nid} deficit {blocker.deficit} < cost {head.cost}"})
                break
            leaf.tasks.popleft()
            for n in path:
                n.deficit -= head.cost
            record["dequeues"].append({
                "leaf": leaf.nid, "task": head.tag, "cost": head.cost,
                "served": True,
                "path_deficits_after": {n.nid: n.deficit for n in path}})

    def done(self) -> bool:
        def fin(n: Node) -> bool:
            if n.is_leaf:
                return bool(n.tasks)
            return any(fin(c) for c in n.children)
        return not fin(self.root)

    # ---- events ----
    def apply_event(self, ev: dict) -> dict:
        op = ev["op"]
        if op == "pause":
            self.pause(ev["node"])
            return {"applied": ev}
        if op == "resume":
            self.resume(ev["node"])
            return {"applied": ev}
        if op == "set_weight":
            self.set_weight(ev["node"], int(ev["weight"]))
            return {"applied": ev}
        if op == "migrate":
            applied = self.migrate(ev["leaf"], ev["new_parent"])
            return {"applied": applied}
        if op == "enqueue":
            leaf = self.node(ev["node"])
            leaf.tasks.append(HTask(cost=int(ev["cost"]),
                                    tag=ev.get("tag", f"{leaf.nid}#{len(leaf.tasks)}")))
            return {"applied": ev}
        raise ValueError(f"unknown event op: {op}")

    def run(self, rounds: int, events: list[dict] | None = None) -> list[dict]:
        events = sorted(events or [], key=lambda e: e["round"])
        out = []
        for _ in range(rounds):
            nxt = self.round_no + 1
            for ev in [e for e in events if e["round"] == nxt]:
                out.append({"event": self.apply_event(ev), "before_round": nxt})
            out.append(self.run_round())
            if self.done() and not any(e["round"] > self.round_no for e in events):
                break
        return out

    # ---- snapshot / restore ----
    def to_snapshot(self) -> dict:
        def enc(n: Node) -> dict:
            return {"id": n.nid, "weight": n.weight, "quantum": n.quantum,
                    "cap": n.cap, "paused": n.paused, "deficit": n.deficit,
                    "tasks": [t.to_dict() for t in n.tasks],
                    "stream": list(n.stream), "stream_pos": n.stream_pos,
                    "children": [enc(c) for c in n.children]}
        return {"version": 1, "round_no": self.round_no, "tree": enc(self.root)}

    @classmethod
    def from_snapshot(cls, snap: dict) -> "HierDRR":
        if snap.get("version") != 1:
            raise ValueError("unsupported snapshot version")

        def dec(d: dict, parent: Node | None) -> Node:
            n = Node(nid=d["id"], weight=d["weight"], quantum=d["quantum"],
                     cap=d["cap"], parent=parent, paused=d["paused"],
                     deficit=d["deficit"])
            n.tasks = deque(HTask(cost=t["cost"], tag=t["tag"]) for t in d["tasks"])
            n.stream = tuple(d.get("stream", ()))
            n.stream_pos = d.get("stream_pos", 0)
            n.children = [dec(c, n) for c in d["children"]]
            return n

        sched = cls(dec(snap["tree"], None))
        sched.round_no = snap["round_no"]
        return sched

    # ---- config ----
    @classmethod
    def from_config(cls, cfg: dict) -> "HierDRR":
        def build(d: dict, parent: Node | None) -> Node:
            n = Node(nid=d["id"], weight=d.get("weight", 1),
                     quantum=d.get("quantum", 1), cap=d["cap"], parent=parent)
            for j, task in enumerate(d.get("tasks", [])):
                if isinstance(task, dict):
                    n.tasks.append(HTask(cost=task["cost"], tag=task.get("tag", f"{n.nid}#{j}")))
                else:
                    n.tasks.append(HTask(cost=int(task), tag=f"{n.nid}#{j}"))
            n.stream = tuple(int(x) for x in d.get("stream", ()))
            n.children = [build(c, n) for c in d.get("children", [])]
            return n

        return cls(build(cfg["tree"], None))
