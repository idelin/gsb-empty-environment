"""Per-device interval store with endpoint indexes.

Every device keeps two sorted indexes for its active records: one ordered by
start and one ordered by end.  Adding or removing a record edits only that
device's lists, so a local correction never scans any other device's
history.  A window query takes the records that start before the window end
and the records that end after the window start and intersects the two sets
with binary searches; records disjoint from the window are never touched.
"""

from __future__ import annotations

from bisect import bisect_left, insort_left
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

from . import intervals

# end is None for membership rules that remain in force indefinitely.
Record = Tuple[str, str, object, Optional[object]]  # (id, device, start, end)


def _end_key(end: Optional[object]) -> Tuple[int, object]:
    return (1, 0) if end is None else (0, end)


@dataclass
class _DeviceIndex:
    records: Dict[str, Record] = field(default_factory=dict)
    starts: List[Tuple[object, str]] = field(default_factory=list)
    ends: List[Tuple[Tuple[int, object], str]] = field(default_factory=list)

    def add(self, record: Record) -> None:
        record_id, _, start, end = record
        self.records[record_id] = record
        insort_left(self.starts, (start, record_id))
        insort_left(self.ends, (_end_key(end), record_id))

    def remove(self, record_id: str) -> Record:
        record = self.records.pop(record_id)
        _, _, start, end = record
        start_pos = bisect_left(self.starts, (start, record_id))
        if start_pos == len(self.starts) or self.starts[start_pos][1] != record_id:
            raise KeyError(record_id)
        end_pos = bisect_left(self.ends, (_end_key(end), record_id))
        if end_pos == len(self.ends) or self.ends[end_pos][1] != record_id:
            raise KeyError(record_id)
        self.starts.pop(start_pos)
        self.ends.pop(end_pos)
        return record

    def get(self, record_id: str) -> Optional[Record]:
        return self.records.get(record_id)

    def all_sorted(self) -> List[Record]:
        return [self.records[record_id] for _, record_id in self.starts]


class Timeline:
    """Holds one kind of record (observations or membership) for all devices."""

    def __init__(self) -> None:
        self._devices: Dict[str, _DeviceIndex] = {}

    def devices(self) -> List[str]:
        return sorted(self._devices)

    def add(self, record_id: str, device: str, start: object, end: Optional[object],
            *, allow_open_end: bool = False) -> Record:
        start, end = intervals.check_interval((start, end), allow_open_end=allow_open_end)
        if self._devices.setdefault(device, _DeviceIndex()).get(record_id) is not None:
            raise KeyError(f"record id {record_id!r} already exists")
        record = (record_id, device, start, end)
        self._devices[device].add(record)
        return record

    def remove(self, record_id: str, device: str) -> Record:
        try:
            return self._devices[device].remove(record_id)
        except KeyError:
            raise KeyError(record_id) from None

    def get(self, record_id: str, device: str) -> Optional[Record]:
        index = self._devices.get(device)
        return index.get(record_id) if index is not None else None

    def raw(self, device: str) -> List[Record]:
        index = self._devices.get(device)
        return index.all_sorted() if index is not None else []

    def covering(self, device: str, window: Tuple[object, object]) -> List[Record]:
        """Active records of one device overlapping the window, via indexes."""
        index = self._devices.get(device)
        if index is None:
            return []
        lo, hi = window
        if lo >= hi:
            return []
        started = {
            record_id for _, record_id in index.starts[:bisect_left(index.starts, (hi, ""))]
        }
        # A record is live when end > lo; open ends (key (1, 0)) sort after
        # every finite end and are always kept.  end == lo is excluded.
        live = {
            record_id
            for _, record_id in index.ends[bisect_left(index.ends, ((0, lo), "\uffff")):]
        }
        ids = started & live
        return [index.records[record_id] for _, record_id in index.starts
                if record_id in ids]

    def active_fragments(self, device: str, window: Tuple[object, object]) -> List[Tuple[object, object]]:
        """Merged, clipped union of a device's records inside the window."""
        records = self.covering(device, window)
        return intervals.clip([(record[2], record[3]) for record in records], window)

    def devices_overlapping(self, window: Tuple[object, object]) -> List[str]:
        return [device for device in self.devices() if self.covering(device, window)]

    def reset(self) -> None:
        self._devices.clear()
