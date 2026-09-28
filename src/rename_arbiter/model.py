"""Event model: stable object IDs, causal deps, and total-order stamps."""

from __future__ import annotations

from dataclasses import dataclass

ROOT_ID = "root"
CONFLICTS_ID = "__conflicts__"
CONFLICTS_NAME = ".conflicts"
RESERVED_IDS = frozenset({ROOT_ID, CONFLICTS_ID})

VALID_TYPES = frozenset({"create", "move", "rename", "delete"})


class EventParseError(ValueError):
    """Raised when an event batch cannot be parsed (bad payload)."""


@dataclass(frozen=True)
class Stamp:
    """Globally unique total-order tie breaker.

    ``value`` is the monotonically comparable part (e.g. a hybrid logical
    clock reading); ``node`` breaks ties when two devices share a value.
    The pair is totally ordered by (value, node), which is exactly the
    ordering required to deterministically arbitrate *concurrent* events.
    """

    value: int
    node: str

    def tuple(self) -> tuple[int, str]:
        return (self.value, self.node)

    def __lt__(self, other: "Stamp") -> bool:
        return self.tuple() < other.tuple()


@dataclass(frozen=True)
class Event:
    id: str
    type: str
    object_id: str
    stamp: Stamp
    deps: frozenset[str]
    is_dir: bool
    new_parent_id: str | None
    new_name: str | None

    @staticmethod
    def from_dict(raw: object, index: int) -> "Event":
        where = f"event #{index}"
        if not isinstance(raw, dict):
            raise EventParseError(f"{where}: must be a JSON object")

        def need_str(key: str) -> str:
            val = raw.get(key)
            if not isinstance(val, str) or not val:
                raise EventParseError(f"{where}: field '{key}' must be a non-empty string")
            return val

        event_id = need_str("id")
        event_type = need_str("type")
        if event_type not in VALID_TYPES:
            raise EventParseError(
                f"{where} ({event_id}): type must be one of {sorted(VALID_TYPES)}"
            )

        object_id = need_str("object_id")
        if object_id in RESERVED_IDS:
            raise EventParseError(f"{where} ({event_id}): object_id '{object_id}' is reserved")

        raw_stamp = raw.get("stamp")
        if not isinstance(raw_stamp, dict):
            raise EventParseError(f"{where} ({event_id}): 'stamp' must be an object")
        value = raw_stamp.get("value")
        node = raw_stamp.get("node")
        if not isinstance(value, int) or isinstance(value, bool):
            raise EventParseError(f"{where} ({event_id}): stamp.value must be an integer")
        if not isinstance(node, str) or not node:
            raise EventParseError(f"{where} ({event_id}): stamp.node must be a non-empty string")

        raw_deps = raw.get("deps", [])
        if not isinstance(raw_deps, list) or not all(isinstance(d, str) and d for d in raw_deps):
            raise EventParseError(f"{where} ({event_id}): deps must be a list of event ids")
        deps = frozenset(raw_deps)
        if event_id in deps:
            raise EventParseError(f"{where} ({event_id}): event may not depend on itself")

        is_dir = raw.get("is_dir", False)
        if not isinstance(is_dir, bool):
            raise EventParseError(f"{where} ({event_id}): is_dir must be true or false")

        new_parent_id = raw.get("new_parent_id")
        if new_parent_id is not None and not isinstance(new_parent_id, str):
            raise EventParseError(f"{where} ({event_id}): new_parent_id must be a string")

        new_name = raw.get("new_name")
        if new_name is not None and (not isinstance(new_name, str) or not new_name):
            raise EventParseError(f"{where} ({event_id}): new_name must be a non-empty string")
        if new_name is not None and (new_name in (".", "..") or "/" in new_name or "\x00" in new_name):
            raise EventParseError(
                f"{where} ({event_id}): new_name may not contain '/', NUL, '.' or '..'"
            )

        if event_type == "create" and new_name is None:
            raise EventParseError(f"{where} ({event_id}): create requires new_name")
        if event_type == "create" and new_parent_id is None:
            raise EventParseError(f"{where} ({event_id}): create requires new_parent_id")
        if event_type == "move" and new_parent_id is None:
            raise EventParseError(f"{where} ({event_id}): move requires new_parent_id")
        if event_type == "rename" and new_name is None:
            raise EventParseError(f"{where} ({event_id}): rename requires new_name")
        if event_type != "create":
            if "is_dir" in raw:
                raise EventParseError(
                    f"{where} ({event_id}): is_dir is only allowed on create events"
                )
        if new_parent_id == ROOT_ID:
            pass
        elif new_parent_id == CONFLICTS_ID:
            raise EventParseError(
                f"{where} ({event_id}): the conflict directory is system-managed"
            )

        return Event(
            id=event_id,
            type=event_type,
            object_id=object_id,
            stamp=Stamp(value, node),
            deps=deps,
            is_dir=is_dir,
            new_parent_id=new_parent_id,
            new_name=new_name,
        )


def parse_events(raw: object) -> list[Event]:
    if not isinstance(raw, list):
        raise EventParseError("input must be a JSON array of events")
    events = [Event.from_dict(item, i) for i, item in enumerate(raw)]
    seen: set[str] = set()
    for ev in events:
        if ev.id in seen:
            raise EventParseError(f"duplicate event id: {ev.id}")
        seen.add(ev.id)
    for ev in events:
        unknown = ev.deps - seen
        if unknown:
            raise EventParseError(
                f"event {ev.id} depends on unknown events: {sorted(unknown)}"
            )
    return events
