#!/usr/bin/env python3
"""
sync.py -- 离线目录树同步：重命名仲裁。

模型
----
- 每个文件系统对象有稳定 ID（创建后不变）。
- 事件：create / rename / move / delete。
- 每个事件带全序决胜戳 stamp = (lamport_clock, replica_id)，全局唯一、全序。
- 因果依赖：事件声明 deps（必须先投递的事件 ID 列表）。
  只有 deps 已全部投递的事件才允许投递（因果合法）。

仲裁规则（确定性，与因果合法的投递顺序无关，因此收敛）
------------------------------------------------------
1. 名字：取该对象 stamp 最大的 create/rename 事件（LWW）。
2. 父目录：取该对象 stamp 最大的 create/move 事件（LWW）。
3. 删除墓碑：delete 事件的 stamp 大于某事件的 stamp 时，该事件被墓碑压制，
   不能复活对象；stamp 比 delete 更新的事件仍然生效。
4. 禁环：若父指针成环，拒绝环上 stamp 最大的那个父指针事件
   （即"最后造成环的移动"），该对象回退到其次新的父指针事件，
   重复直至无环。拒绝原因被记录。
5. 同名冲突：同一目录下多个存活对象同名时，名字事件 stamp 最大者胜出，
   败者移入 /.conflicts/ 冲突目录并记录冲突原因。
6. 孤儿：父目录不存在或已删除的对象挂回根目录并记录。

用法
----
    python3 sync.py apply    events.json [--json]
    python3 sync.py converge events.json [--limit N]

apply    按文件顺序投递（校验因果合法性），输出最终状态。
converge 枚举所有因果合法的投递顺序，验证全部收敛到同一状态。
"""

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from typing import Iterator, Optional

ROOT = "root"
CONFLICTS = ".conflicts"
OPS = ("create", "rename", "move", "delete")


# ---------------------------------------------------------------- events

@dataclass(frozen=True)
class Event:
    id: str
    op: str
    obj: str
    clock: int
    replica: str
    deps: frozenset = frozenset()
    name: Optional[str] = None
    parent: Optional[str] = None

    @property
    def stamp(self) -> tuple:
        """全序决胜戳：(lamport clock, replica id)。"""
        return (self.clock, self.replica)

    @staticmethod
    def from_dict(d: dict) -> "Event":
        for key in ("id", "op", "obj", "clock", "replica"):
            if key not in d:
                raise ValueError(f"event missing required field {key!r}: {d!r}")
        if d["op"] not in OPS:
            raise ValueError(f"unknown op {d['op']!r} in event {d['id']!r}")
        return Event(
            id=str(d["id"]),
            op=d["op"],
            obj=str(d["obj"]),
            clock=int(d["clock"]),
            replica=str(d["replica"]),
            deps=frozenset(str(x) for x in d.get("deps", [])),
            name=d.get("name"),
            parent=d.get("parent"),
        )


def load_events(path: str) -> list:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    raw = data["events"] if isinstance(data, dict) else data
    events = [Event.from_dict(d) for d in raw]
    ids = [e.id for e in events]
    if len(ids) != len(set(ids)):
        raise ValueError("duplicate event ids in input")
    known = set(ids) | {ROOT}
    for e in events:
        unknown = e.deps - set(ids)
        if unknown:
            raise ValueError(f"event {e.id} depends on unknown events {sorted(unknown)}")
    return events


# ---------------------------------------------------------------- state

@dataclass
class State:
    parents: dict = field(default_factory=dict)   # obj -> parent obj | ROOT | CONFLICTS
    names: dict = field(default_factory=dict)     # obj -> effective display name
    paths: dict = field(default_factory=dict)     # obj -> absolute path
    tombstones: dict = field(default_factory=dict)  # obj -> delete stamp
    conflicts: list = field(default_factory=list)   # name-conflict records
    rejected: list = field(default_factory=list)    # rejected events (cycle)
    orphans: list = field(default_factory=list)     # reattached-to-root records

    def canonical(self) -> str:
        return json.dumps(self.to_dict(), sort_keys=True, ensure_ascii=False)

    def to_dict(self) -> dict:
        return {
            "tree": {p: o for o, p in sorted(self.paths.items())},
            "conflicts": sorted(self.conflicts, key=lambda c: (c["obj"], c["reason"])),
            "rejected": sorted(self.rejected, key=lambda r: r["event"]),
            "orphans": sorted(self.orphans, key=lambda o: o["obj"]),
            "tombstones": {o: list(s) for o, s in sorted(self.tombstones.items())},
        }


# ---------------------------------------------------------------- node

class SyncNode:
    """接收事件并物化最终状态。物化是事件集合的纯函数，故与投递顺序无关。"""

    def __init__(self) -> None:
        self.events: dict = {}

    def can_deliver(self, ev: Event) -> bool:
        return ev.id not in self.events and all(d in self.events for d in ev.deps)

    def deliver(self, ev: Event) -> None:
        if ev.id in self.events:
            raise ValueError(f"duplicate delivery of event {ev.id}")
        missing = sorted(d for d in ev.deps if d not in self.events)
        if missing:
            raise ValueError(
                f"causal violation: event {ev.id} delivered before deps {missing}"
            )
        self.events[ev.id] = ev

    # -- materialization ------------------------------------------------

    def materialize(self) -> State:
        by_obj = defaultdict(list)
        for ev in self.events.values():
            by_obj[ev.obj].append(ev)

        alive: dict = {}
        tombstones: dict = {}
        for obj, evs in by_obj.items():
            del_stamps = [e.stamp for e in evs if e.op == "delete"]
            del_stamp = max(del_stamps) if del_stamps else None
            if del_stamp is not None:
                tombstones[obj] = del_stamp
            # 墓碑压制所有比它旧的事件；比它新的事件仍然生效
            live_ops = [
                e for e in evs
                if e.op != "delete" and (del_stamp is None or e.stamp > del_stamp)
            ]
            if not live_ops:
                continue
            name_evs = [e for e in live_ops if e.op in ("create", "rename")]
            name_ev = max(name_evs, key=lambda e: e.stamp) if name_evs else None
            parent_evs = sorted(
                (e for e in live_ops if e.op in ("create", "move")),
                key=lambda e: e.stamp,
                reverse=True,
            )
            alive[obj] = {
                "name": name_ev.name if name_ev and name_ev.name else obj,
                "name_stamp": name_ev.stamp if name_ev else (0, ""),
                "parent_evs": parent_evs,
            }

        state = State(tombstones=tombstones)
        rejected_ids: set = set()

        def current_parent(obj: str):
            """对象当前生效的 (parent, event)，跳过已被拒绝的事件。"""
            for e in alive[obj]["parent_evs"]:
                if e.id not in rejected_ids:
                    return (e.parent if e.parent else ROOT), e
            return ROOT, None

        # 禁环：反复找环，拒绝环上 stamp 最大的父指针事件（最后造成环者）
        while True:
            parents, orphans = {}, []
            for obj in alive:
                parent, _ev = current_parent(obj)
                if parent != ROOT and parent not in alive:
                    orphans.append({
                        "obj": obj,
                        "requested_parent": parent,
                        "reason": "parent missing or deleted; reattached to root",
                    })
                    parent = ROOT
                parents[obj] = parent
            cycle = _find_cycle(parents)
            if cycle is None:
                break
            culprit, culprit_ev = None, None
            for obj in cycle:
                _p, ev = current_parent(obj)
                if ev is not None and (culprit_ev is None or ev.stamp > culprit_ev.stamp):
                    culprit, culprit_ev = obj, ev
            if culprit_ev is None:  # 理论上不可达：环上必有父指针事件
                raise AssertionError("cycle without any parent event")
            rejected_ids.add(culprit_ev.id)
            state.rejected.append({
                "event": culprit_ev.id,
                "obj": culprit,
                "op": culprit_ev.op,
                "reason": "rejected: would create directory cycle "
                          + " -> ".join(cycle + [cycle[0]]),
            })

        state.orphans = orphans

        # 同名冲突：同目录同名，名字事件 stamp 最大者胜，败者进冲突目录
        groups = defaultdict(list)
        for obj in alive:
            groups[(parents[obj], alive[obj]["name"])].append(obj)
        for (parent, name), objs in sorted(groups.items()):
            if parent == CONFLICTS or len(objs) < 2:
                continue
            objs.sort(key=lambda o: alive[o]["name_stamp"], reverse=True)
            winner = objs[0]
            for loser in objs[1:]:
                parents[loser] = CONFLICTS
                state.conflicts.append({
                    "obj": loser,
                    "name": name,
                    "winner": winner,
                    "reason": (
                        f"name-conflict: {loser!r} and {winner!r} both named "
                        f"{name!r} in directory {parent!r}; {winner!r} wins "
                        f"(higher stamp {list(alive[winner]['name_stamp'])}), "
                        f"{loser!r} moved to /{CONFLICTS}/"
                    ),
                })

        # 冲突目录内若仍撞名，用对象 ID 消歧
        final_names = {o: alive[o]["name"] for o in alive}
        used = defaultdict(list)
        for o in alive:
            used[(parents[o], final_names[o])].append(o)
        for (parent, name), objs in used.items():
            if len(objs) > 1:
                for o in sorted(objs):
                    final_names[o] = f"{name}#{o}"

        state.parents = parents
        state.names = final_names
        state.paths = {o: _path_of(o, parents, final_names) for o in alive}
        return state


def _find_cycle(parents: dict) -> Optional[list]:
    for start in sorted(parents):
        path: list = []
        node = start
        while node in parents:
            if node in path:
                return path[path.index(node):]
            path.append(node)
            node = parents[node]
    return None


def _path_of(obj: str, parents: dict, names: dict) -> str:
    parts: list = []
    node = obj
    for _ in range(len(parents) + 2):
        if node == ROOT:
            break
        if node == CONFLICTS:
            parts.append(CONFLICTS)
            break
        parts.append(names[node])
        node = parents.get(node, ROOT)
    return "/" + "/".join(reversed(parts))


# ---------------------------------------------------------------- causal orders

def causal_orders(events: list) -> Iterator[list]:
    """枚举所有因果合法（拓扑）投递顺序。"""
    by_id = {e.id: e for e in events}
    remaining = set(by_id)
    done: set = set()
    order: list = []

    def backtrack() -> Iterator[list]:
        if not remaining:
            yield list(order)
            return
        for eid in sorted(remaining):
            ev = by_id[eid]
            if ev.deps <= done:
                remaining.discard(eid)
                done.add(eid)
                order.append(eid)
                yield from backtrack()
                order.pop()
                done.discard(eid)
                remaining.add(eid)

    yield from backtrack()


def check_convergence(events: list, limit: int = 20000) -> dict:
    """枚举因果投递顺序（至多 limit 种），验证全部物化为同一状态。"""
    by_id = {e.id: e for e in events}
    reference: Optional[str] = None
    tested = 0
    truncated = False
    for order in causal_orders(events):
        if tested >= limit:
            truncated = True
            break
        node = SyncNode()
        for eid in order:
            node.deliver(by_id[eid])
        canon = node.materialize().canonical()
        if reference is None:
            reference = canon
        elif canon != reference:
            return {"status": "diverged", "orders_tested": tested + 1,
                    "order": order}
        tested += 1
    node = SyncNode()
    for e in events:
        node.deliver(e)
    return {
        "status": "truncated" if truncated else "converged",
        "orders_tested": tested,
        "state": node.materialize().to_dict(),
    }


# ---------------------------------------------------------------- CLI

def _print_state(state_dict: dict) -> None:
    print("== final tree ==")
    for path, obj in state_dict["tree"].items():
        print(f"  {path}   (id={obj})")
    print("== conflicts ==")
    for c in state_dict["conflicts"]:
        print(f"  [{c['obj']}] {c['reason']}")
    if not state_dict["conflicts"]:
        print("  (none)")
    print("== rejected events ==")
    for r in state_dict["rejected"]:
        print(f"  [{r['event']}] obj={r['obj']} op={r['op']}: {r['reason']}")
    if not state_dict["rejected"]:
        print("  (none)")
    print("== orphans ==")
    for o in state_dict["orphans"]:
        print(f"  [{o['obj']}] {o['reason']}")
    if not state_dict["orphans"]:
        print("  (none)")
    print("== tombstones ==")
    for obj, stamp in state_dict["tombstones"].items():
        print(f"  {obj} deleted at stamp {stamp}")
    if not state_dict["tombstones"]:
        print("  (none)")


def main(argv: Optional[list] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_apply = sub.add_parser("apply", help="按文件顺序投递事件并输出最终状态")
    p_apply.add_argument("file")
    p_apply.add_argument("--json", action="store_true", help="以 JSON 输出")

    p_conv = sub.add_parser("converge", help="枚举所有因果投递顺序并验证收敛")
    p_conv.add_argument("file")
    p_conv.add_argument("--limit", type=int, default=20000,
                        help="最多枚举的投递顺序数（默认 20000）")
    p_conv.add_argument("--json", action="store_true", help="以 JSON 输出")

    args = parser.parse_args(argv)
    try:
        events = load_events(args.file)
    except (ValueError, KeyError, json.JSONDecodeError) as exc:
        print(f"error: invalid input: {exc}", file=sys.stderr)
        return 2

    if args.cmd == "apply":
        node = SyncNode()
        try:
            for ev in events:
                node.deliver(ev)
        except ValueError as exc:
            print(f"error: {exc}", file=sys.stderr)
            return 2
        state = node.materialize().to_dict()
        if args.json:
            print(json.dumps(state, indent=2, ensure_ascii=False))
        else:
            _print_state(state)
        return 0

    result = check_convergence(events, limit=args.limit)
    if args.json:
        print(json.dumps(result, indent=2, ensure_ascii=False))
    else:
        print(f"status: {result['status']} "
              f"({result['orders_tested']} causal orders tested)")
        if result["status"] == "diverged":
            print(f"first diverging order: {result['order']}")
        else:
            _print_state(result["state"])
    return 0 if result["status"] == "converged" else 1


if __name__ == "__main__":
    sys.exit(main())
