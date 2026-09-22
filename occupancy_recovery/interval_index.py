"""Dynamic interval index used to maintain occupancy without rebuilding scans.

The index is a randomized treap over known coordinates. Each treap node stores
one coordinate x and the elementary segment beginning at x. Range additions and
removals are lazy canonical updates on treap splits.
"""

from __future__ import annotations

from dataclasses import dataclass
import random
from typing import Any, Dict, Hashable, List, Optional, Set, Tuple


@dataclass(frozen=True)
class EvidenceSegment:
    start: Any
    end: Any
    occupancy: int
    occupancy_ids: Tuple[Hashable, ...]


@dataclass(frozen=True)
class PeakPeriod:
    start: Any
    end: Any
    occupancy: int


@dataclass(frozen=True)
class PeakQueryResult:
    start: Any
    end: Any
    peak: int
    periods: Tuple[PeakPeriod, ...]
    evidence_segments: Tuple[EvidenceSegment, ...]


class TreapNode:
    __slots__ = (
        "key",
        "priority",
        "left",
        "right",
        "members",
        "pending",
        "peak",
        "size",
        "min_key",
        "max_key",
    )

    def __init__(
        self,
        key: Any,
        priority: int,
        members: Optional[Set[Hashable]] = None,
    ) -> None:
        self.key = key
        self.priority = priority
        self.left: Optional[TreapNode] = None
        self.right: Optional[TreapNode] = None
        self.members: Set[Hashable] = set() if members is None else set(members)
        self.pending: Dict[Hashable, int] = {}
        self.peak = len(self.members)
        self.size = 1
        self.min_key = key
        self.max_key = key


def _pull(node: TreapNode) -> None:
    node.peak = len(node.members)
    node.size = 1
    node.min_key = node.key
    node.max_key = node.key

    if node.left is not None:
        node.peak = max(node.peak, node.left.peak)
        node.size += node.left.size
        node.min_key = node.left.min_key
    if node.right is not None:
        node.peak = max(node.peak, node.right.peak)
        node.size += node.right.size
        node.max_key = node.right.max_key


def _apply(node: TreapNode, changes: Dict[Hashable, int]) -> None:
    for occupancy_id, delta in changes.items():
        if delta > 0:
            if occupancy_id in node.members:
                continue
            node.members.add(occupancy_id)
            node.peak += 1
            if node.pending.get(occupancy_id) == -1:
                del node.pending[occupancy_id]
            elif occupancy_id not in node.pending:
                node.pending[occupancy_id] = 1
        elif delta < 0:
            if occupancy_id not in node.members:
                continue
            node.members.remove(occupancy_id)
            node.peak -= 1
            if node.pending.get(occupancy_id) == 1:
                del node.pending[occupancy_id]
            elif occupancy_id not in node.pending:
                node.pending[occupancy_id] = -1


def _push(node: TreapNode) -> None:
    if not node.pending:
        return

    changes = node.pending
    if node.left is not None:
        _apply(node.left, changes)
    if node.right is not None:
        _apply(node.right, changes)
    node.pending = {}


def _split(
    root: Optional[TreapNode], key: Any
) -> Tuple[Optional[TreapNode], Optional[TreapNode]]:
    if root is None:
        return None, None

    _push(root)
    if root.key < key:
        left, right = _split(root.right, key)
        root.right = left
        _pull(root)
        return root, right

    left, right = _split(root.left, key)
    root.left = right
    _pull(root)
    return left, root


def _merge(
    left: Optional[TreapNode], right: Optional[TreapNode]
) -> Optional[TreapNode]:
    if left is None:
        return right
    if right is None:
        return left

    if left.priority > right.priority:
        _push(left)
        left.right = _merge(left.right, right)
        _pull(left)
        return left

    _push(right)
    right.left = _merge(left, right.left)
    _pull(right)
    return right


class DynamicIntervalIndex:
    """Maintains active IDs for all elementary segments between coordinates."""

    def __init__(self, seed: int = 0) -> None:
        self._rng = random.Random(seed)
        self._root: Optional[TreapNode] = None
        self._coord_refs: Dict[Any, int] = {}

    @property
    def size(self) -> int:
        return 0 if self._root is None else self._root.size

    def _new_priority(self) -> int:
        return self._rng.getrandbits(64)

    def _predecessor_members(self, key: Any) -> Set[Hashable]:
        node = self._root
        members: Set[Hashable] = set()

        while node is not None:
            _push(node)
            if key < node.key:
                node = node.left
            else:
                members = set(node.members)
                node = node.right

        return members

    def _insert_coordinate(self, key: Any) -> None:
        if key in self._coord_refs:
            return

        members = self._predecessor_members(key)
        node = TreapNode(key, self._new_priority(), members)

        def insert(
            root: Optional[TreapNode],
        ) -> TreapNode:
            if root is None:
                return node

            _push(root)
            if node.priority > root.priority:
                left, right = _split(root, key)
                node.left = left
                node.right = right
                _pull(node)
                return node

            if key < root.key:
                root.left = insert(root.left)
            else:
                root.right = insert(root.right)
            _pull(root)
            return root

        self._root = insert(self._root)

    def _delete_coordinate(self, key: Any) -> None:
        def delete(
            root: Optional[TreapNode],
        ) -> Optional[TreapNode]:
            if root is None:
                return None

            _push(root)
            if key == root.key:
                return _merge(root.left, root.right)
            if key < root.key:
                root.left = delete(root.left)
            else:
                root.right = delete(root.right)
            _pull(root)
            return root

        self._root = delete(self._root)

    def _acquire_coordinate(self, key: Any) -> None:
        reference_count = self._coord_refs.get(key, 0)
        if reference_count == 0:
            self._insert_coordinate(key)
        self._coord_refs[key] = reference_count + 1

    def _release_coordinate(self, key: Any) -> None:
        reference_count = self._coord_refs.get(key, 0)
        if reference_count <= 1:
            self._coord_refs.pop(key, None)
            self._delete_coordinate(key)
        else:
            self._coord_refs[key] = reference_count - 1

    def add_interval(self, occupancy_id: Hashable, start: Any, end: Any) -> None:
        if not start < end:
            raise ValueError("interval must be half-open with start < end")

        self._acquire_coordinate(start)
        self._acquire_coordinate(end)
        left, middle_right = _split(self._root, start)
        middle, right = _split(middle_right, end)
        if middle is None:
            raise RuntimeError("internal interval index error: missing interval range")
        _apply(middle, {occupancy_id: 1})
        self._root = _merge(left, _merge(middle, right))

    def remove_interval(
        self, occupancy_id: Hashable, start: Any, end: Any
    ) -> None:
        if not start < end:
            raise ValueError("interval must be half-open with start < end")

        left, middle_right = _split(self._root, start)
        middle, right = _split(middle_right, end)
        if middle is None:
            raise RuntimeError("internal interval index error: missing interval range")
        _apply(middle, {occupancy_id: -1})
        self._root = _merge(left, _merge(middle, right))
        self._release_coordinate(start)
        self._release_coordinate(end)

    def peak(self, start: Any, end: Any) -> PeakQueryResult:
        return self.query(start, end)

    def query(self, start: Any, end: Any) -> PeakQueryResult:
        if not start < end:
            raise ValueError("query range must be half-open with start < end")

        inserted_start = start not in self._coord_refs
        inserted_end = end not in self._coord_refs
        if inserted_start:
            self._insert_coordinate(start)
        if inserted_end:
            self._insert_coordinate(end)

        left, middle_right = _split(self._root, start)
        middle, right = _split(middle_right, end)
        if middle is None:
            raise RuntimeError("internal interval index error: missing query range")

        peak_value = middle.peak
        raw_segments: List[EvidenceSegment] = []

        if peak_value == 0:
            raw_segments.append(
                EvidenceSegment(start, end, 0, tuple())
            )
        else:
            current: Optional[TreapNode] = middle
            stack: List[TreapNode] = []

            while current is not None or stack:
                while current is not None:
                    _push(current)
                    stack.append(current)
                    current = current.left

                current = stack.pop()
                if len(current.members) == peak_value:
                    if current.right is not None:
                        segment_end = current.right.min_key
                    elif stack:
                        segment_end = stack[-1].key
                    else:
                        segment_end = end

                    raw_segments.append(
                        EvidenceSegment(
                            current.key,
                            segment_end,
                            peak_value,
                            tuple(sorted(current.members, key=repr)),
                        )
                    )

                if current.right is not None and current.right.peak == peak_value:
                    current = current.right
                else:
                    current = None

        self._root = _merge(left, _merge(middle, right))
        if inserted_end:
            self._delete_coordinate(end)
        if inserted_start:
            self._delete_coordinate(start)

        evidence = tuple(raw_segments)
        periods: List[PeakPeriod] = []
        for segment in evidence:

            if (
                periods
                and periods[-1].end == segment.start
                and periods[-1].occupancy == segment.occupancy
            ):
                previous = periods[-1]
                periods[-1] = PeakPeriod(
                    previous.start, segment.end, previous.occupancy
                )
            else:
                periods.append(
                    PeakPeriod(
                        segment.start, segment.end, segment.occupancy
                    )
                )

        return PeakQueryResult(
            start,
            end,
            peak_value,
            tuple(periods),
            evidence,
        )
