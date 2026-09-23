"""Gap tracking: park corrections whose parent versions are not yet known.

This is deliberately *only* about waiting. A correction parked here is paused,
never misapplied; conflict judgement lives in ``conflict_detector``.
"""
from __future__ import annotations

from .models import Correction


class GapTracker:
    def __init__(self) -> None:
        self._waiting: dict[str, Correction] = {}

    @staticmethod
    def missing_parents(correction: Correction, known_versions: set[str]) -> list[str]:
        return [p for p in correction.parents if p not in known_versions]

    def park(self, correction: Correction) -> None:
        self._waiting[correction.revision_id] = correction

    def unpark(self, revision_id: str) -> Correction | None:
        return self._waiting.pop(revision_id, None)

    def items(self):
        return self._waiting.items()

    def waiting_corrections(self) -> dict[str, Correction]:
        return dict(self._waiting)

    def __contains__(self, revision_id: str) -> bool:
        return revision_id in self._waiting

    def __len__(self) -> int:
        return len(self._waiting)
