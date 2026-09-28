"""Offline rename arbitration engine.

Guarantees
----------
* *Convergence*: any delivery order that respects ``deps`` produces the
  identical final tree, conflict directory and report.  Delivery order is
  irrelevant because events are replayed in a canonical linear extension
  of the causal DAG (Kahn's algorithm with a stamp-ordered ready heap).
* *Concurrent same-name conflict*: the entry whose placing event has the
  larger total-order stamp keeps the name; the loser is moved, whole
  subtree included, into ``.conflicts/`` with an explanatory record.
* *No cycles*: moving a directory into itself or one of its descendants
  is rejected with reason ``CYCLE``.
* *Tombstones*: deletes win over stale events.  An object can only come
  back through an explicit ``create`` whose dependencies already contain
  the tombstone; concurrent or older recreations are rejected.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field

from .model import (
    CONFLICTS_ID,
    CONFLICTS_NAME,
    ROOT_ID,
    Event,
)

# Rejection reasons ----------------------------------------------------------
R_BAD_TARGET = "TARGET_NOT_DIRECTORY"
R_NO_OBJECT = "OBJECT_NOT_FOUND"
R_TARGET_MISSING = "TARGET_NOT_FOUND"
R_DELETED = "TARGET_DELETED"
R_STALE_CREATE = "OBJECT_ALREADY_EXISTS"
R_STALE_REVIVE = "CONCURRENT_RECREATE_REJECTED"
R_CYCLE = "CYCLE"
R_DUP_DELETE = "ALREADY_DELETED"

# Conflict reasons -----------------------------------------------------------
C_CONCURRENT = "CONCURRENT_NAME_COLLISION"
C_CAUSAL = "CAUSAL_NAME_SUPERSESSION"


class CausalCycleError(ValueError):
    """Raised when the dependency relation contains a cycle."""


@dataclass
class _Object:
    object_id: str
    is_dir: bool
    live: bool
    parent_id: str | None
    name: str | None
    placed_by: str | None
    deleted_by: str | None
    in_conflict: bool = False


@dataclass(frozen=True)
class Rejection:
    event_id: str
    object_id: str
    reason: str
    detail: str


@dataclass(frozen=True)
class Conflict:
    reason: str
    parent_id: str
    name: str
    winner_object_id: str
    loser_object_id: str
    winner_event_id: str
    loser_event_id: str
    winner_stamp: tuple[int, str]
    loser_stamp: tuple[int, str]
    conflict_path: str


@dataclass
class ResolutionResult:
    events: list[Event] = field(default_factory=list)
    order: list[str] = field(default_factory=list)
    conflicts: list[Conflict] = field(default_factory=list)
    rejections: list[Rejection] = field(default_factory=list)
    objects: dict[str, _Object] = field(default_factory=dict)

    def _placed_event(self, event_id: str) -> Event:
        return next(e for e in self.events if e.id == event_id)

    def to_dict(self) -> dict:
        return {
            "order": list(self.order),
            "tree": self._tree(),
            "placements": self._placements(),
            "tombstones": self._tombstones(),
            "conflicts": [
                {
                    "reason": c.reason,
                    "parent_id": c.parent_id,
                    "name": c.name,
                    "winner": {
                        "object_id": c.winner_object_id,
                        "event_id": c.winner_event_id,
                        "stamp": {"value": c.winner_stamp[0], "node": c.winner_stamp[1]},
                    },
                    "loser": {
                        "object_id": c.loser_object_id,
                        "event_id": c.loser_event_id,
                        "stamp": {"value": c.loser_stamp[0], "node": c.loser_stamp[1]},
                    },
                    "conflict_path": c.conflict_path,
                    "message": _conflict_message(c),
                }
                for c in self.conflicts
            ],
            "rejected": [
                {"event_id": r.event_id, "object_id": r.object_id, "reason": r.reason, "detail": r.detail}
                for r in self.rejections
            ],
            "summary": {
                "events_total": len(self.events),
                "events_applied": len(self.order) - len(self.rejections),
                "events_rejected": len(self.rejections),
                "conflicts": len(self.conflicts),
            },
        }

    # -- output assembly ----------------------------------------------------
    def _placements(self) -> list[dict]:
        rows = []
        for obj in self._live_sorted():
            rows.append(
                {
                    "object_id": obj.object_id,
                    "is_dir": obj.is_dir,
                    "parent_id": obj.parent_id,
                    "name": obj.name,
                    "placed_by": obj.placed_by,
                    "in_conflict": obj.in_conflict,
                }
            )
        return rows

    def _tombstones(self) -> list[dict]:
        return [
            {"object_id": obj.object_id, "is_dir": obj.is_dir, "deleted_by": obj.deleted_by}
            for obj in sorted(self.objects.values(), key=lambda o: o.object_id)
            if not obj.live
        ]

    def _live_sorted(self) -> list[_Object]:
        return sorted(
            (o for o in self.objects.values() if o.live),
            key=lambda o: (tuple(_path_keys(self, o.parent_id)), o.name, o.object_id),
        )

    def _tree(self) -> dict:
        node = {
            "object_id": ROOT_ID,
            "name": "",
            "is_dir": True,
            "children": [],
        }
        nodes: dict[str, dict] = {ROOT_ID: node}

        cnode = {
            "object_id": CONFLICTS_ID,
            "name": CONFLICTS_NAME,
            "is_dir": True,
            "conflict_dir": True,
            "children": [],
        }
        nodes[CONFLICTS_ID] = cnode

        for obj in self._live_sorted():
            nodes[obj.object_id] = {
                "object_id": obj.object_id,
                "name": obj.name,
                "is_dir": obj.is_dir,
                "in_conflict": obj.in_conflict,
                "children": [],
            }

        for obj in self._live_sorted():
            parent = nodes.get(obj.parent_id)
            if parent is not None:
                parent["children"].append(nodes[obj.object_id])

        if cnode["children"]:
            node["children"].insert(0, cnode)
        return node


def _path_keys(result: ResolutionResult, parent_id: str | None) -> list[str]:
    keys: list[str] = []
    seen: set[str] = set()
    cur = parent_id
    while cur and cur not in (ROOT_ID, CONFLICTS_ID) and cur not in seen:
        seen.add(cur)
        obj = result.objects.get(cur)
        if obj is None or not obj.live:
            break
        keys.append(obj.name or cur)
        cur = obj.parent_id
    keys.reverse()
    return keys


def _conflict_message(c: Conflict) -> str:
    if c.reason == C_CONCURRENT:
        return (
            f"concurrent same-name conflict in {c.parent_id} for '{c.name}': "
            f"event {c.winner_event_id} stamp ({c.winner_stamp[0]},{c.winner_stamp[1]}) "
            f"beats event {c.loser_event_id} stamp ({c.loser_stamp[0]},{c.loser_stamp[1]}); "
            f"{c.loser_object_id} moved to {c.conflict_path}"
        )
    return (
        f"causal same-name supersession in {c.parent_id} for '{c.name}': "
            f"event {c.winner_event_id} causally follows {c.loser_event_id}; "
            f"{c.loser_object_id} moved to {c.conflict_path}"
        )


class SyncEngine:
    """Replays an event batch into a converged tree."""

    def __init__(self) -> None:
        self.objects: dict[str, _Object] = {}
        # (parent_id, name) -> object_id, only for non-conflict live entries
        self._by_name: dict[tuple[str, str], str] = {}
        # parent_id -> set of live child object ids
        self._children: dict[str, set[str]] = {}
        self._events_by_id: dict[str, Event] = {}
        self._conflicts: list[Conflict] = []
        self._rejections: list[Rejection] = []
        self._conflict_seq = 0

    def resolve(self, events: list[Event]) -> ResolutionResult:
        """Resolve a batch. ``events`` may be supplied in any causal order.

        Re-delivery (replaying the same batch, possibly reordered) yields
        byte-identical reports.
        """
        self.__init__()
        for ev in events:
            self._events_by_id[ev.id] = ev

        order = self._canonical_order(events)
        for event_id in order:
            ev = self._events_by_id[event_id]
            handler = {
                "create": self._apply_create,
                "move": self._apply_move,
                "rename": self._apply_rename,
                "delete": self._apply_delete,
            }[ev.type]
            handler(ev)

        return ResolutionResult(
            events=list(events),
            order=order,
            conflicts=list(self._conflicts),
            rejections=list(self._rejections),
            objects=self.objects,
        )

    # -- causal ordering ----------------------------------------------------
    def _canonical_order(self, events: list[Event]) -> list[str]:
        """Return a deterministic linear extension of the causal DAG.

        Ready events are popped by stamp (then event id); every causal
        predecessor is applied first, and concurrent events are arbitrated
        in the same global order regardless of delivery.
        """
        remaining_deps: dict[str, set[str]] = {ev.id: set(ev.deps) for ev in events}
        dependents: dict[str, set[str]] = {ev.id: set() for ev in events}
        for ev in events:
            for dep in ev.deps:
                dependents[dep].add(ev.id)

        ready: list[tuple] = []
        for ev in events:
            if not remaining_deps[ev.id]:
                heapq.heappush(ready, (ev.stamp.tuple(), ev.id))

        order: list[str] = []
        while ready:
            _, event_id = heapq.heappop(ready)
            order.append(event_id)
            for child in dependents[event_id]:
                remaining_deps[child].discard(event_id)
                if not remaining_deps[child]:
                    cev = self._events_by_id[child]
                    heapq.heappush(ready, (cev.stamp.tuple(), child))

        if len(order) != len(events):
            stuck = sorted(remaining_deps)
            raise CausalCycleError(f"dependency cycle detected among events: {stuck}")
        return order

    def _ancestors(self, event_id: str) -> set[str]:
        """Transitive closure of ``deps`` for one event."""
        out: set[str] = set()
        stack = list(self._events_by_id[event_id].deps)
        while stack:
            dep = stack.pop()
            if dep in out or dep not in self._events_by_id:
                continue
            out.add(dep)
            stack.extend(self._events_by_id[dep].deps)
        return out

    # -- index helpers ------------------------------------------------------
    def _live(self, object_id: str) -> _Object | None:
        obj = self.objects.get(object_id)
        return obj if obj is not None and obj.live else None

    def _index_add(self, obj: _Object) -> None:
        assert obj.parent_id is not None and obj.name is not None
        self._children.setdefault(obj.parent_id, set()).add(obj.object_id)
        if not obj.in_conflict:
            self._by_name[(obj.parent_id, obj.name)] = obj.object_id

    def _index_remove(self, obj: _Object) -> None:
        if obj.parent_id is not None:
            kids = self._children.get(obj.parent_id)
            if kids is not None:
                kids.discard(obj.object_id)
        if not obj.in_conflict:
            self._by_name.pop((obj.parent_id or "", obj.name or ""), None)

    def _reject(self, ev: Event, reason: str, detail: str) -> None:
        self._rejections.append(Rejection(ev.id, ev.object_id, reason, detail))

    # -- conflict arbitration ----------------------------------------------
    def _conflict_name(self, obj: _Object, ev: Event) -> str:
        self._conflict_seq += 1
        base = (obj.name or "unnamed").replace("/", "_")
        safe_event = ev.id.replace("/", "_")
        return f"{base}__{obj.object_id}__{safe_event}-{self._conflict_seq}"

    def _move_to_conflict(self, obj: _Object, losing_event: Event) -> str:
        self._index_remove(obj)
        obj.parent_id = CONFLICTS_ID
        obj.name = self._conflict_name(obj, losing_event)
        obj.in_conflict = True
        obj.placed_by = losing_event.id
        self._index_add(obj)
        return f"/{CONFLICTS_NAME}/{obj.name}"

    def _arbitrate(
        self,
        ev: Event,
        obj: _Object,
        parent_id: str,
        name: str,
    ) -> None:
        """Place live ``obj`` at (parent_id, name), arbitrating collisions.

        Incoming event loses to a resident whose placement is in its causal
        past; otherwise stamps decide.  The loser (with its whole subtree)
        is relocated into the conflict directory.
        """
        resident_id = self._by_name.get((parent_id, name))
        if resident_id is None:
            self._index_remove(obj)
            obj.parent_id = parent_id
            obj.name = name
            obj.in_conflict = False
            obj.placed_by = ev.id
            self._index_add(obj)
            return

        if resident_id == obj.object_id:
            self._index_remove(obj)
            obj.parent_id = parent_id
            obj.name = name
            obj.in_conflict = False
            obj.placed_by = ev.id
            self._index_add(obj)
            return

        resident = self.objects[resident_id]
        resident_event = self._events_by_id[resident.placed_by]
        incoming_after = resident.placed_by in self._ancestors(ev.id)

        if incoming_after:
            incoming_wins = True
            reason = C_CAUSAL
        else:
            incoming_wins = ev.stamp.tuple() > resident_event.stamp.tuple()
            reason = C_CONCURRENT

        if incoming_wins:
            loser_obj, loser_event = resident, resident_event
            winner_obj, winner_event = obj, ev
        else:
            loser_obj, loser_event = obj, ev
            winner_obj, winner_event = resident, resident_event

        conflict_path = self._move_to_conflict(loser_obj, loser_event)
        self._conflicts.append(
            Conflict(
                reason=reason,
                parent_id=parent_id,
                name=name,
                winner_object_id=winner_obj.object_id,
                loser_object_id=loser_obj.object_id,
                winner_event_id=winner_event.id,
                loser_event_id=loser_event.id,
                winner_stamp=winner_event.stamp.tuple(),
                loser_stamp=loser_event.stamp.tuple(),
                conflict_path=conflict_path,
            )
        )

        if incoming_wins:
            self._index_remove(obj)
            obj.parent_id = parent_id
            obj.name = name
            obj.in_conflict = False
            obj.placed_by = ev.id
            self._index_add(obj)

    # -- target validation --------------------------------------------------
    def _require_live_dir(self, ev: Event, parent_id: str) -> _Object | None:
        if parent_id == ROOT_ID:
            return _Object(ROOT_ID, True, True, None, None, None, None)
        target = self.objects.get(parent_id)
        if target is None:
            self._reject(ev, R_TARGET_MISSING, f"parent '{parent_id}' does not exist")
            return None
        if not target.live:
            self._reject(ev, R_DELETED, f"parent '{parent_id}' is a tombstone")
            return None
        if not target.is_dir:
            self._reject(ev, R_BAD_TARGET, f"parent '{parent_id}' is not a directory")
            return None
        return target

    def _is_ancestor_dir(self, ancestor_id: str, descendant_id: str) -> bool:
        cur: str | None = descendant_id
        seen: set[str] = set()
        while cur and cur not in seen:
            if cur == ancestor_id:
                return True
            seen.add(cur)
            obj = self.objects.get(cur)
            cur = obj.parent_id if obj is not None else None
        return False

    # -- event handlers -----------------------------------------------------
    def _apply_create(self, ev: Event) -> None:
        target = self._require_live_dir(ev, ev.new_parent_id)
        if target is None:
            return

        existing = self.objects.get(ev.object_id)
        if existing is not None:
            if existing.live:
                self._reject(ev, R_STALE_CREATE, "object id is already live")
                return
            if existing.deleted_by not in self._ancestors(ev.id):
                self._reject(
                    ev,
                    R_STALE_REVIVE,
                    "concurrent/older create cannot resurrect a tombstone",
                )
                return

        obj = _Object(
            object_id=ev.object_id,
            is_dir=ev.is_dir,
            live=True,
            parent_id=None,
            name=None,
            placed_by=ev.id,
            deleted_by=None,
        )
        self.objects[ev.object_id] = obj
        self._arbitrate(ev, obj, ev.new_parent_id, ev.new_name)

    def _apply_move(self, ev: Event) -> None:
        obj = self._live(ev.object_id)
        if obj is None:
            if self.objects.get(ev.object_id) is not None:
                self._reject(ev, R_DUP_DELETE, "object is a tombstone")
            else:
                self._reject(ev, R_NO_OBJECT, "object has never been created")
            return
        target = self._require_live_dir(ev, ev.new_parent_id)
        if target is None:
            return

        if obj.parent_id == ev.new_parent_id:
            return

        if obj.is_dir and self._is_ancestor_dir(ev.object_id, ev.new_parent_id):
            self._reject(
                ev,
                R_CYCLE,
                f"refusing to move directory '{ev.object_id}' into its own descendant "
                f"'{ev.new_parent_id}'",
            )
            return

        self._arbitrate(ev, obj, ev.new_parent_id, obj.name)

    def _apply_rename(self, ev: Event) -> None:
        obj = self._live(ev.object_id)
        if obj is None:
            if self.objects.get(ev.object_id) is not None:
                self._reject(ev, R_DUP_DELETE, "object is a tombstone")
            else:
                self._reject(ev, R_NO_OBJECT, "object has never been created")
            return

        if obj.name == ev.new_name:
            return

        self._arbitrate(ev, obj, obj.parent_id, ev.new_name)

    def _delete_subtree(self, root_obj: _Object, ev: Event) -> list[_Object]:
        killed: list[_Object] = []
        stack = [root_obj]
        while stack:
            cur = stack.pop()
            if not cur.live:
                continue
            for child_id in list(self._children.get(cur.object_id, ())):
                child = self.objects.get(child_id)
                if child is not None and child.live:
                    stack.append(child)
            self._index_remove(cur)
            cur.live = False
            cur.parent_id = None
            cur.name = None
            cur.in_conflict = False
            cur.deleted_by = ev.id
            killed.append(cur)
        self._children.pop(root_obj.object_id, None)
        return killed

    def _apply_delete(self, ev: Event) -> None:
        obj = self.objects.get(ev.object_id)
        if obj is None:
            self._reject(ev, R_NO_OBJECT, "object has never been created")
            return
        if not obj.live:
            self._reject(ev, R_DUP_DELETE, "object is already a tombstone")
            return
        self._delete_subtree(obj, ev)
