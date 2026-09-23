from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping


class CDCError(ValueError):
    pass


class InvalidFixtureError(CDCError):
    pass


class WatermarkRegressionError(CDCError):
    pass


class _Absent:
    _instance = None

    def __new__(cls) -> "_Absent":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "<absent>"


ABSENT = _Absent()


@dataclass(frozen=True, slots=True)
class EntityRef:
    table: str
    primary_key: Any

    def __post_init__(self) -> None:
        if not isinstance(self.table, str) or not self.table:
            raise InvalidFixtureError("table must be a non-empty string")
        try:
            hash(self.primary_key)
        except TypeError as exc:
            raise InvalidFixtureError("primary_key must be hashable") from exc


@dataclass(frozen=True, slots=True)
class ChangeEvent:
    transaction_id: str
    table: str
    primary_key: Any
    before_value: Any
    after_value: Any
    commit_seq: int

    @property
    def entity(self) -> EntityRef:
        return EntityRef(self.table, self.primary_key)

    @property
    def event_key(self) -> "EventKey":
        return EventKey(self.transaction_id, self.table, self.primary_key)

    def __post_init__(self) -> None:
        if not isinstance(self.transaction_id, str) or not self.transaction_id:
            raise InvalidFixtureError("transaction_id must be a non-empty string")
        EntityRef(self.table, self.primary_key)
        if isinstance(self.commit_seq, bool) or not isinstance(self.commit_seq, int):
            raise InvalidFixtureError("commit_seq must be an integer")
        if self.commit_seq < 0:
            raise InvalidFixtureError("commit_seq must be non-negative")


@dataclass(frozen=True, slots=True)
class EventKey:
    transaction_id: str
    table: str
    primary_key: Any

    @property
    def entity(self) -> EntityRef:
        return EntityRef(self.table, self.primary_key)


@dataclass(frozen=True, slots=True)
class EntitySummary:
    entity: EntityRef
    events: tuple[ChangeEvent, ...]
    final_value: Any


@dataclass(frozen=True, slots=True)
class Replica:
    name: str
    high_water_mark: int
    events: tuple[ChangeEvent, ...]

    def __post_init__(self) -> None:
        if not isinstance(self.name, str) or not self.name:
            raise InvalidFixtureError("replica name must be a non-empty string")
        if (
            isinstance(self.high_water_mark, bool)
            or not isinstance(self.high_water_mark, int)
        ):
            raise InvalidFixtureError("high_water_mark must be an integer")
        if self.high_water_mark < 0:
            raise InvalidFixtureError("high_water_mark must be non-negative")

        events = tuple(self.events)
        for event in events:
            if not isinstance(event, ChangeEvent):
                raise InvalidFixtureError("all events must be ChangeEvent instances")
            if event.commit_seq > self.high_water_mark:
                raise InvalidFixtureError(
                    f"event {event.event_key} is above replica high water mark"
                )

        seen: set[EventKey] = set()
        transaction_sequences: dict[str, set[int]] = {}
        entity_sequences: dict[EntityRef, set[int]] = {}
        for event in events:
            if event.event_key in seen:
                raise InvalidFixtureError(f"duplicate event {event.event_key}")
            seen.add(event.event_key)
            transaction_sequences.setdefault(event.transaction_id, set()).add(
                event.commit_seq
            )
            entity_sequences.setdefault(event.entity, set()).add(event.commit_seq)

        for transaction_id, sequences in transaction_sequences.items():
            if len(sequences) != 1:
                raise InvalidFixtureError(
                    f"transaction {transaction_id} contains multiple commit sequences"
                )
        for entity, sequences in entity_sequences.items():
            if len(sequences) != len(
                [event for event in events if event.entity == entity]
            ):
                raise InvalidFixtureError(
                    f"entity {entity} has multiple events at one commit sequence"
                )

        object.__setattr__(self, "events", events)


@dataclass(frozen=True, slots=True)
class MissingEvent:
    entity: EntityRef
    event_key: EventKey
    commit_seq: int
    side: str
    before_value: Any
    after_value: Any
    source_position: int


@dataclass(frozen=True, slots=True)
class OrderConflict:
    entity: EntityRef
    event_key: EventKey
    left_commit_seq: int
    right_commit_seq: int


@dataclass(frozen=True, slots=True)
class EventValueDivergence:
    entity: EntityRef
    event_key: EventKey
    commit_seq: int
    left_before: Any
    right_before: Any
    left_after: Any
    right_after: Any


@dataclass(frozen=True, slots=True)
class FinalValueDivergence:
    entity: EntityRef
    left_value: Any
    right_value: Any


@dataclass(frozen=True, slots=True)
class Lag:
    side: str
    replica_name: str
    requested_watermark: int
    replica_high_water_mark: int


@dataclass(frozen=True, slots=True)
class ComparisonResult:
    status: str
    common_watermark: int
    left_name: str
    right_name: str
    lag: Lag | None
    missing_events: tuple[MissingEvent, ...] = ()
    order_conflicts: tuple[OrderConflict, ...] = ()
    event_value_divergences: tuple[EventValueDivergence, ...] = ()
    final_value_divergences: tuple[FinalValueDivergence, ...] = ()


def build_entity_summaries(
    events: tuple[ChangeEvent, ...] | list[ChangeEvent],
) -> dict[EntityRef, EntitySummary]:
    grouped: dict[EntityRef, list[ChangeEvent]] = {}
    for event in events:
        grouped.setdefault(event.entity, []).append(event)

    summaries: dict[EntityRef, EntitySummary] = {}
    for entity, entity_events in grouped.items():
        ordered = tuple(sorted(entity_events, key=lambda event: event.commit_seq))
        summaries[entity] = EntitySummary(
            entity=entity,
            events=ordered,
            final_value=ordered[-1].after_value,
        )
    return summaries


def snapshot(
    replica: Replica, common_watermark: int
) -> Mapping[EntityRef, EntitySummary]:
    _validate_watermark(common_watermark)
    if common_watermark > replica.high_water_mark:
        raise InvalidFixtureError(
            f"common watermark {common_watermark} is above "
            f"{replica.name} high water mark {replica.high_water_mark}"
        )
    visible = tuple(
        event for event in replica.events if event.commit_seq <= common_watermark
    )
    return build_entity_summaries(visible)


def compare_replicas(
    left: Replica,
    right: Replica,
    common_watermark: int | None = None,
) -> ComparisonResult:
    if left.name == right.name:
        raise InvalidFixtureError("replica names must be different")

    if common_watermark is None:
        target = min(left.high_water_mark, right.high_water_mark)
        lag_record = None
    else:
        _validate_watermark(common_watermark)
        target = common_watermark
        lag_record = _lag_if_needed(left, right, target)
        if lag_record is not None:
            return ComparisonResult(
                status="lagging",
                common_watermark=target,
                left_name=left.name,
                right_name=right.name,
                lag=lag_record,
            )

    left_snapshot = snapshot(left, target)
    right_snapshot = snapshot(right, target)
    return _compare_snapshots(left, right, target, left_snapshot, right_snapshot)


class ComparisonSession:
    def __init__(self) -> None:
        self._last_watermarks: dict[frozenset[str], int] = {}

    def compare(
        self,
        left: Replica,
        right: Replica,
        common_watermark: int | None = None,
    ) -> ComparisonResult:
        target = common_watermark
        if target is None:
            target = min(left.high_water_mark, right.high_water_mark)
        _validate_watermark(target)

        pair = frozenset((left.name, right.name))
        previous = self._last_watermarks.get(pair)
        if previous is not None and target < previous:
            raise WatermarkRegressionError(
                f"common watermark for pair {sorted(pair)} cannot move backwards: "
                f"{target} < {previous}"
            )

        result = compare_replicas(left, right, common_watermark)
        if result.status != "lagging":
            self._last_watermarks[pair] = result.common_watermark
        return result


def _lag_if_needed(left: Replica, right: Replica, target: int) -> Lag | None:
    if left.high_water_mark < target:
        return Lag("left", left.name, target, left.high_water_mark)
    if right.high_water_mark < target:
        return Lag("right", right.name, target, right.high_water_mark)
    return None


def _validate_watermark(value: int) -> None:
    if isinstance(value, bool) or not isinstance(value, int):
        raise InvalidFixtureError("common watermark must be an integer")
    if value < 0:
        raise InvalidFixtureError("common watermark must be non-negative")


def _compare_snapshots(
    left: Replica,
    right: Replica,
    target: int,
    left_snapshot: Mapping[EntityRef, EntitySummary],
    right_snapshot: Mapping[EntityRef, EntitySummary],
) -> ComparisonResult:
    missing: list[MissingEvent] = []
    order_conflicts: list[OrderConflict] = []
    event_divergences: list[EventValueDivergence] = []
    final_divergences: list[FinalValueDivergence] = []

    entities = sorted(
        set(left_snapshot) | set(right_snapshot),
        key=lambda entity: (entity.table, repr(entity.primary_key)),
    )

    for entity in entities:
        left_summary = left_snapshot.get(entity)
        right_summary = right_snapshot.get(entity)
        left_events = left_summary.events if left_summary else ()
        right_events = right_summary.events if right_summary else ()
        left_by_key = {event.event_key: event for event in left_events}
        right_by_key = {event.event_key: event for event in right_events}

        for event_key in sorted(
            set(left_by_key) | set(right_by_key),
            key=lambda key: (
                min(
                    left_by_key[key].commit_seq
                    if key in left_by_key
                    else right_by_key[key].commit_seq,
                    right_by_key[key].commit_seq
                    if key in right_by_key
                    else left_by_key[key].commit_seq,
                ),
                key.transaction_id,
            ),
        ):
            left_event = left_by_key.get(event_key)
            right_event = right_by_key.get(event_key)

            if left_event is None and right_event is not None:
                missing.append(
                    MissingEvent(
                        entity=entity,
                        event_key=event_key,
                        commit_seq=right_event.commit_seq,
                        side="left",
                        before_value=right_event.before_value,
                        after_value=right_event.after_value,
                        source_position=_position(right_events, right_event),
                    )
                )
            elif right_event is None and left_event is not None:
                missing.append(
                    MissingEvent(
                        entity=entity,
                        event_key=event_key,
                        commit_seq=left_event.commit_seq,
                        side="right",
                        before_value=left_event.before_value,
                        after_value=left_event.after_value,
                        source_position=_position(left_events, left_event),
                    )
                )
            elif left_event is not None and right_event is not None:
                if left_event.commit_seq != right_event.commit_seq:
                    order_conflicts.append(
                        OrderConflict(
                            entity=entity,
                            event_key=event_key,
                            left_commit_seq=left_event.commit_seq,
                            right_commit_seq=right_event.commit_seq,
                        )
                    )
                if (
                    left_event.before_value != right_event.before_value
                    or left_event.after_value != right_event.after_value
                ):
                    event_divergences.append(
                        EventValueDivergence(
                            entity=entity,
                            event_key=event_key,
                            commit_seq=left_event.commit_seq,
                            left_before=left_event.before_value,
                            right_before=right_event.before_value,
                            left_after=left_event.after_value,
                            right_after=right_event.after_value,
                        )
                    )

        left_value = left_summary.final_value if left_summary else ABSENT
        right_value = right_summary.final_value if right_summary else ABSENT
        if left_value != right_value:
            final_divergences.append(
                FinalValueDivergence(
                    entity=entity,
                    left_value=left_value,
                    right_value=right_value,
                )
            )

    has_difference = bool(
        missing or order_conflicts or event_divergences or final_divergences
    )
    return ComparisonResult(
        status="divergent" if has_difference else "consistent",
        common_watermark=target,
        left_name=left.name,
        right_name=right.name,
        lag=None,
        missing_events=tuple(missing),
        order_conflicts=tuple(order_conflicts),
        event_value_divergences=tuple(event_divergences),
        final_value_divergences=tuple(final_divergences),
    )


def _position(events: tuple[ChangeEvent, ...], event: ChangeEvent) -> int:
    return events.index(event) + 1
