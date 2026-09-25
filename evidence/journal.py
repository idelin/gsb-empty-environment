"""Append-only evidence journal with stable step IDs and versions."""
from __future__ import annotations

import hashlib
import json


class Journal:
    """Records solver steps so any result can be replayed and verified."""

    STEP_VERSION = 1

    def __init__(self) -> None:
        self.steps: list[dict] = []
        self._counter = 0

    def record(self, kind: str, payload: dict) -> str:
        self._counter += 1
        step = {
            "step_id": f"step-{self._counter:04d}",
            "version": self.STEP_VERSION,
            "kind": kind,
            "payload": payload,
        }
        self.steps.append(step)
        return step["step_id"]

    def digest(self) -> str:
        blob = json.dumps(self.steps, sort_keys=True, default=str)
        return hashlib.sha256(blob.encode("utf-8")).hexdigest()
