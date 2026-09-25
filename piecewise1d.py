from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass
import math
from typing import Iterable

EPS = 1e-12


class PiecewiseError(ValueError): pass
class OutsideDomainError(PiecewiseError): pass
class InterpolationGapError(PiecewiseError): pass


def real(value: object) -> float:
    if not isinstance(value, (int, float)) or isinstance(value, bool):
        raise TypeError("a real number is required")
    value = float(value)
    if math.isnan(value) or math.isinf(value):
        raise ValueError("a finite real number is required")
    return value


@dataclass(frozen=True)
class Measurement:
    lower: float
    upper: float
    def __post_init__(self):
        lo, hi = real(self.lower), real(self.upper)
        if lo > hi:
            raise ValueError("measurement interval is empty")
        object.__setattr__(self, "lower", lo)
        object.__setattr__(self, "upper", hi)
    @property
    def nominal(self) -> float:
        return (self.lower + self.upper) * 0.5


@dataclass(frozen=True)
class SourceRef:
    node_id: str
    x: float
    role: str


@dataclass(frozen=True)
class Evidence:
    evidence_id: int
    kind: str
    channel: int
    left: SourceRef
    right: SourceRef
    created_revision: int
    revoked_revision: int | None = None
    @property
    def revoked(self) -> bool:
        return self.revoked_revision is not None


@dataclass(frozen=True)
class Node:
    node_id: str
    channel: int
    x: float
    measurement: Measurement | None
    priority: int = 0
    version: int = 1
    hard_break: bool = False


@dataclass(frozen=True)
class Feature:
    x: float
    measurement: Measurement
    source: SourceRef
    hard_break: bool


@dataclass(frozen=True)
class Barrier:
    x: float
    source: SourceRef


Entry = Feature | Barrier


@dataclass(frozen=True)
class Segment:
    x0: float
    x1: float
    y0_low: float
    y0_high: float
    y1_low: float
    y1_high: float
    left: SourceRef
    right: SourceRef
    evidence_id: int
    left_hard_break: bool = False


@dataclass(frozen=True)
class Snapshot:
    revision: int
    channel_count: int
    entries: tuple[tuple[Entry, ...], ...]
    segments: tuple[tuple[Segment, ...], ...]


@dataclass(frozen=True)
class ValueResult:
    lower: float
    upper: float
    nominal: float
    sources: tuple[SourceRef, ...]
    evidence_id: int | None


@dataclass(frozen=True)
class DerivativeResult:
    lower: float
    upper: float
    left: float | None
    right: float | None
    sources: tuple[SourceRef, ...]
    evidence_id: int | None


@dataclass(frozen=True)
class InverseSolution:
    lower: float
    upper: float
    kind: str
    sources: tuple[SourceRef, ...]
    evidence_ids: tuple[int, ...]


@dataclass(frozen=True)
class ChangeReport:
    revision: int
    rebuilt_channels: tuple[int, ...]
    rebuilt_segments: int
    revoked_evidence: tuple[int, ...]
    created_evidence: tuple[int, ...]

def _source(node: Node, role: str) -> SourceRef:
    return SourceRef(node.node_id, float(node.x), role)


def _winner_key(node: Node):
    return (-node.priority, -node.version, node.node_id)


def _winner(nodes: Iterable[Node]) -> Node:
    return min(nodes, key=_winner_key)


def _entry(node: Node) -> Entry:
    source = _source(node, "point")
    if node.measurement is None:
        return Barrier(float(node.x), source)
    return Feature(float(node.x), node.measurement, source, node.hard_break)


def _entry_key(entry: Entry) -> float:
    return entry.x


def _structure(segment: Segment):
    return (
        segment.x0, segment.x1,
        segment.y0_low, segment.y0_high,
        segment.y1_low, segment.y1_high,
        segment.left.node_id, segment.right.node_id, segment.left_hard_break,
    )


class PiecewiseModel:
    def __init__(self, channel_count: int):
        if not isinstance(channel_count, int) or isinstance(channel_count, bool) or channel_count < 1:
            raise ValueError("channel_count must be a positive integer")
        self.channel_count = channel_count
        self._nodes: dict[str, Node] = {}
        self._tombstones: dict[str, Node] = {}
        self._evidence: dict[int, Evidence] = {}
        self._next_evidence = 1
        self.revision = 0
        self.snapshot = Snapshot(0, channel_count, tuple(() for _ in range(channel_count)),
                                 tuple(() for _ in range(channel_count)))

    def _validate_channel(self, channel: int) -> None:
        if not isinstance(channel, int) or isinstance(channel, bool) or not 0 <= channel < self.channel_count:
            raise IndexError("channel index is outside explicit channel_count")

    def _validate_node(self, node: Node) -> None:
        if not isinstance(node.node_id, str) or not node.node_id:
            raise ValueError("node_id must be a non-empty string")
        self._validate_channel(node.channel)
        real(node.x)
        if not isinstance(node.priority, int) or isinstance(node.priority, bool):
            raise ValueError("priority must be an integer")
        if not isinstance(node.version, int) or isinstance(node.version, bool) or node.version < 1:
            raise ValueError("version must be a positive integer")
        if not isinstance(node.hard_break, bool):
            raise ValueError("hard_break must be bool")
        if node.measurement is not None and not isinstance(node.measurement, Measurement):
            raise TypeError("measurement must be Measurement or None")

    def _channel_entries(self, channel: int) -> tuple[Entry, ...]:
        groups: dict[float, list[Node]] = {}
        for node in self._nodes.values():
            if node.channel == channel:
                groups.setdefault(real(node.x), []).append(node)
        return tuple(sorted((_entry(_winner(group)) for group in groups.values()), key=_entry_key))

    def _issue_segment(self, channel: int, left: Feature, right: Feature, revision: int) -> Segment:
        eid = self._next_evidence
        self._next_evidence += 1
        segment = Segment(
            left.x, right.x,
            left.measurement.lower, left.measurement.upper,
            right.measurement.lower, right.measurement.upper,
            left.source, right.source, eid, left.hard_break,
        )
        self._evidence[eid] = Evidence(eid, "segment", channel, left.source, right.source, revision)
        return segment

    def _revoke(self, segment: Segment, revision: int) -> None:
        old = self._evidence[segment.evidence_id]
        self._evidence[old.evidence_id] = Evidence(
            old.evidence_id, old.kind, old.channel, old.left, old.right,
            old.created_revision, revision,
        )

    def _pairs(self, entries: tuple[Entry, ...]):
        for index in range(len(entries) - 1):
            left, right = entries[index], entries[index + 1]
            if (
                isinstance(left, Feature)
                and isinstance(right, Feature)
                and right.x > left.x
                and not left.hard_break and not right.hard_break
            ):
                yield index, left, right

    def _rebuild_channel(self, channel: int, entries: tuple[Entry, ...], revision: int,
                         old_segments: tuple[Segment, ...]):
        old_by_structure = {_structure(item): item for item in old_segments}
        wanted_structures = {
            (
                left.x, right.x,
                left.measurement.lower, left.measurement.upper,
                right.measurement.lower, right.measurement.upper,
                left.source.node_id, right.source.node_id, left.hard_break,
            )
            for _, left, right in self._pairs(entries)
        }
        revoked = []
        for old in old_segments:
            if _structure(old) not in wanted_structures:
                self._revoke(old, revision)
                revoked.append(old.evidence_id)
        segments = []
        created = []
        for _, left, right in self._pairs(entries):
            structure = (
                left.x, right.x,
                left.measurement.lower, left.measurement.upper,
                right.measurement.lower, right.measurement.upper,
                left.source.node_id, right.source.node_id, left.hard_break,
            )
            old = old_by_structure.get(structure)
            if old is not None:
                segments.append(old)
            else:
                segment = self._issue_segment(channel, left, right, revision)
                segments.append(segment)
                created.append(segment.evidence_id)
        segments.sort(key=lambda item: (item.x0, item.x1))
        return tuple(segments), tuple(revoked), tuple(created)

    def _mutate(self, channels: set[int]) -> ChangeReport:
        self.revision += 1
        revision = self.revision
        entries = list(self.snapshot.entries)
        segments = list(self.snapshot.segments)
        rebuilt_channels = []
        revoked_all = []
        created_all = []
        rebuilt_count = 0
        for channel in sorted(channels):
            new_entries = self._channel_entries(channel)
            new_segments, revoked, created = self._rebuild_channel(
                channel, new_entries, revision, segments[channel]
            )
            entries[channel] = new_entries
            segments[channel] = new_segments
            rebuilt_channels.append(channel)
            rebuilt_count += len(revoked) + len(created)
            revoked_all.extend(revoked)
            created_all.extend(created)
        self.snapshot = Snapshot(revision, self.channel_count, tuple(entries), tuple(segments))
        return ChangeReport(revision, tuple(rebuilt_channels), rebuilt_count,
                            tuple(revoked_all), tuple(created_all))

    def add_node(self, node: Node) -> ChangeReport:
        self._validate_node(node)
        if node.node_id in self._nodes or node.node_id in self._tombstones:
            raise KeyError("node_id already exists; use calibrate or restore")
        self._nodes[node.node_id] = node
        return self._mutate({node.channel})

    def calibrate(self, node_id: str, *, x: float | None = None,
                  measurement: Measurement | None | object = ...,
                  priority: int | None = None, version: int | None = None,
                  hard_break: bool | None = None) -> ChangeReport:
        try:
            old = self._nodes[node_id]
        except KeyError:
            raise KeyError("node is not active") from None
        new_version = old.version + 1 if version is None else version
        if not isinstance(new_version, int) or new_version <= old.version:
            raise ValueError("calibrate requires a larger explicit version")
        updated = Node(
            old.node_id,
            old.channel,
            old.x if x is None else real(x),
            old.measurement if measurement is ... else measurement,
            old.priority if priority is None else priority,
            new_version,
            old.hard_break if hard_break is None else hard_break,
        )
        self._validate_node(updated)
        self._nodes[node_id] = updated
        return self._mutate({old.channel, updated.channel})

    def delete_node(self, node_id: str) -> ChangeReport:
        try:
            old = self._nodes.pop(node_id)
        except KeyError:
            raise KeyError("node is not active") from None
        self._tombstones[node_id] = old
        return self._mutate({old.channel})

    def restore_node(self, node_id: str) -> ChangeReport:
        try:
            old = self._tombstones.pop(node_id)
        except KeyError:
            raise KeyError("node is not deleted") from None
        self._nodes[node_id] = old
        return self._mutate({old.channel})

    def evidence(self, evidence_id: int) -> Evidence:
        return self._evidence[evidence_id]

    def _check_channel(self, channel: int) -> None:
        if not isinstance(channel, int) or isinstance(channel, bool) or not 0 <= channel < self.channel_count:
            raise IndexError("channel index is outside explicit channel_count")

    def _entry_at(self, entries: tuple[Entry, ...], x: float):
        position = bisect_left(entries, x, key=_entry_key)
        if position < len(entries) and entries[position].x == x:
            return entries[position], position
        return None, position

    @staticmethod
    def _segment_for(segments: tuple[Segment, ...], x: float) -> Segment | None:
        position = bisect_left(segments, x, key=lambda item: item.x0)
        if position < len(segments) and segments[position].x0 == x:
            return segments[position]
        if position > 0:
            candidate = segments[position - 1]
            if candidate.x0 < x <= candidate.x1:
                return candidate
        return None

    def value_at(self, channel: int, x: float, snapshot: Snapshot | None = None) -> ValueResult:
        self._check_channel(channel)
        x = real(x)
        snap = self.snapshot if snapshot is None else snapshot
        entry, position = self._entry_at(snap.entries[channel], x)
        if entry is not None:
            if isinstance(entry, Barrier):
                raise InterpolationGapError("coordinate is an unknown measurement")
            measurement = entry.measurement
            return ValueResult(measurement.lower, measurement.upper, measurement.nominal,
                               (entry.source,), None)
        if not snap.entries[channel] or x < snap.entries[channel][0].x or x > snap.entries[channel][-1].x:
            raise OutsideDomainError("x is outside node domain; extrapolation is disabled")
        segment = self._segment_for(snap.segments[channel], x)
        if segment is None:
            raise InterpolationGapError("a break or unknown node separates the endpoints")
        width = segment.x1 - segment.x0
        t = (x - segment.x0) / width
        low = segment.y0_low * (1.0 - t) + segment.y1_low * t
        high = segment.y0_high * (1.0 - t) + segment.y1_high * t
        return ValueResult(min(low, high), max(low, high), (low + high) * 0.5,
                           (segment.left, segment.right), segment.evidence_id)

    @staticmethod
    def _left_segment(segments: tuple[Segment, ...], x: float) -> Segment | None:
        candidates = [item for item in segments if item.x1 == x]
        return candidates[-1] if candidates else None

    @staticmethod
    def _right_segment(segments: tuple[Segment, ...], x: float) -> Segment | None:
        candidates = [item for item in segments if item.x0 == x]
        return candidates[0] if candidates else None

    @staticmethod
    def _slope_bounds(segment: Segment):
        width = segment.x1 - segment.x0
        slopes = (
            (segment.y1_low - segment.y0_low) / width,
            (segment.y1_low - segment.y0_high) / width,
            (segment.y1_high - segment.y0_low) / width,
            (segment.y1_high - segment.y0_high) / width,
        )
        return min(slopes), max(slopes), slopes[0]

    def derivative_at(self, channel: int, x: float, snapshot: Snapshot | None = None) -> DerivativeResult:
        self._check_channel(channel)
        x = real(x)
        snap = self.snapshot if snapshot is None else snapshot
        entries = snap.entries[channel]
        entry, _ = self._entry_at(entries, x)
        if entry is not None:
            if isinstance(entry, Barrier):
                raise InterpolationGapError("unknown node has no derivative")
            left_segment = self._left_segment(snap.segments[channel], x)
            right_segment = self._right_segment(snap.segments[channel], x)
            adjacent = [item for item in (left_segment, right_segment) if item is not None]
            if not adjacent:
                raise InterpolationGapError("no adjacent interpolable segment")
            bounds = [bound for item in adjacent for bound in self._slope_bounds(item)[:2]]
            sources = []
            for item in adjacent:
                sources.extend((item.left, item.right))
            left_value = None if left_segment is None else self._slope_bounds(left_segment)[2]
            right_value = None if right_segment is None else self._slope_bounds(right_segment)[2]
            return DerivativeResult(min(bounds), max(bounds), left_value, right_value,
                                    tuple(dict.fromkeys(sources)), None)
        value = self.value_at(channel, x, snap)
        if value.evidence_id is None:
            raise InterpolationGapError("point derivative requires a node entry")
        segment = next(item for item in snap.segments[channel] if item.evidence_id == value.evidence_id)
        low, high, slope = self._slope_bounds(segment)
        return DerivativeResult(low, high, slope, slope,
                                (segment.left, segment.right), segment.evidence_id)

    @staticmethod
    def _constrain_le(constant: float, slope: float, bound: float, current: list[float]) -> list[float] | None:
        rhs = bound - constant
        low, high = current
        if abs(slope) <= EPS:
            return current if constant <= bound + EPS else None
        root = rhs / slope
        if slope > 0.0:
            high = min(high, root)
        else:
            low = max(low, root)
        if low - EPS > high:
            return None
        low = max(0.0, low)
        high = min(1.0, high)
        if low <= high + EPS:
            return [low, min(1.0, high)]
        return None

    def _segment_inverse(self, segment: Segment, target: float, include_uncertain: bool):
        if include_uncertain:
            bounds = [0.0, 1.0]
            bounds = self._constrain_le(segment.y0_low, segment.y1_low - segment.y0_low, target, bounds)
            if bounds is None:
                return None
            bounds = self._constrain_le(-segment.y0_high, segment.y0_high - segment.y1_high, -target, bounds)
            if bounds is None:
                return None
            t0, t1 = bounds
            nominal0 = (segment.y0_low + segment.y0_high) * 0.5
            nominal1 = (segment.y1_low + segment.y1_high) * 0.5
            whole = t0 <= EPS and t1 >= 1.0 - EPS
            deterministic_flat = (
                abs(segment.y0_low - segment.y0_high) <= EPS
                and abs(segment.y1_low - segment.y1_high) <= EPS
                and abs(nominal0 - nominal1) <= EPS
                and abs(target - nominal0) <= EPS
            )
            if whole and deterministic_flat:
                kind = "plateau"
            elif whole:
                kind = "uncertain_range"
            else:
                kind = "point" if t1 - t0 <= EPS else "range"
        else:
            nominal0 = (segment.y0_low + segment.y0_high) * 0.5
            nominal1 = (segment.y1_low + segment.y1_high) * 0.5
            if abs(nominal1 - nominal0) <= EPS:
                if abs(target - nominal0) > EPS:
                    return None
                t0, t1, kind = 0.0, 1.0, "plateau"
            else:
                t0 = t1 = (target - nominal0) / (nominal1 - nominal0)
                if t0 < -EPS or t0 > 1.0 + EPS:
                    return None
                t0 = max(0.0, min(1.0, t0))
                t1 = t0
                kind = "point"
        x0 = segment.x0 + (segment.x1 - segment.x0) * t0
        x1 = segment.x0 + (segment.x1 - segment.x0) * t1
        return x0, x1, kind

    def inverse_at(self, channel: int, target: float, snapshot: Snapshot | None = None,
                   include_uncertain: bool = True) -> tuple[InverseSolution, ...]:
        self._check_channel(channel)
        target = real(target)
        snap = self.snapshot if snapshot is None else snapshot
        entries = snap.entries[channel]
        if not entries or target != target:
            raise OutsideDomainError("channel has no nodes or target is NaN")
        raw = []
        for segment in snap.segments[channel]:
            found = self._segment_inverse(segment, target, include_uncertain)
            if found is not None:
                x0, x1, kind = found
                raw.append({
                    "low": min(x0, x1),
                    "high": max(x0, x1),
                    "kind": kind,
                    "sources": [segment.left, segment.right],
                    "evidence": [segment.evidence_id],
                })
        for entry in entries:
            if isinstance(entry, Feature):
                contains = entry.measurement.lower - EPS <= target <= entry.measurement.upper + EPS if include_uncertain else abs(target - entry.measurement.nominal) <= EPS
                if contains and not any(item["low"] - EPS <= entry.x <= item["high"] + EPS for item in raw):
                    raw.append({
                        "low": entry.x,
                        "high": entry.x,
                        "kind": "point",
                        "sources": [entry.source],
                        "evidence": [],
                    })
        raw.sort(key=lambda item: (item["low"], item["high"]))
        merged = []
        for item in raw:
            if not merged or item["low"] > merged[-1]["high"] + EPS:
                merged.append(item)
                continue
            current = merged[-1]
            current["low"] = min(current["low"], item["low"])
            current["high"] = max(current["high"], item["high"])
            current["sources"].extend(item["sources"])
            current["evidence"].extend(item["evidence"])
            rank = {"point": 0, "range": 1, "plateau": 2, "uncertain_range": 3}
            if rank[item["kind"]] > rank[current["kind"]]:
                current["kind"] = item["kind"]
        solutions = []
        for item in merged:
            sources = tuple(dict.fromkeys(item["sources"]))
            evidence = tuple(sorted(set(item["evidence"])))
            width = item["high"] - item["low"]
            kind = item["kind"]
            if kind not in {"plateau", "uncertain_range"} and width <= EPS:
                kind = "point"
            solutions.append(InverseSolution(item["low"], item["high"], kind, sources, evidence))
        return tuple(solutions)
