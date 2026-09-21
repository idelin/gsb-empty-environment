"""File-backed spill for reorder-buffer overflow, with fault injection."""
from __future__ import annotations

import json
from pathlib import Path

from .events import Event


class BackpressureError(Exception):
    """The layer refuses the event; nothing was lost, caller must retry."""


class SpillError(BackpressureError):
    """Spill write failed (e.g. disk pressure); event not accepted."""


class CapacityError(BackpressureError):
    """Memory + spill capacity exhausted; event not accepted."""


class SpillBackend:
    def __init__(self, directory: Path):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        # Fault injection knob: number of upcoming writes that must fail.
        self.fail_next_writes = 0

    def _file(self, partition: str, epoch: int, cycle: int, seq: int) -> Path:
        return self.directory / f"{partition}-{epoch}-{cycle}-{seq}.json"

    def clear(self) -> None:
        """Remove every spill file; replay re-creates the ones still needed."""
        for path in self.directory.glob("*.json"):
            path.unlink(missing_ok=True)
        for path in self.directory.glob("*.tmp"):
            path.unlink(missing_ok=True)

    def write(self, event: Event) -> None:
        if self.fail_next_writes > 0:
            self.fail_next_writes -= 1
            raise SpillError("injected spill write failure")
        target = self._file(event.partition, event.epoch, event.cycle, event.seq)
        with open(target.with_suffix(".tmp"), "w", encoding="utf-8") as fh:
            json.dump(event.to_dict(), fh)
        target.with_suffix(".tmp").replace(target)

    def read(self, partition: str, epoch: int, cycle: int, seq: int) -> Event:
        with open(self._file(partition, epoch, cycle, seq), "r", encoding="utf-8") as fh:
            return Event.from_dict(json.load(fh))

    def remove(self, partition: str, epoch: int, cycle: int, seq: int) -> None:
        self._file(partition, epoch, cycle, seq).unlink(missing_ok=True)
