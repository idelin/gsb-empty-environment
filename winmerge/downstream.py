"""Simulated downstream consumer with idempotent delta application.

State lives in a single JSON file written atomically (tmp + rename).
Applying a publish payload twice is a no-op: the second call reports
``duplicate: True`` and does not move the totals.  This is what makes
the merger's at-least-once recovery safe.
"""
from __future__ import annotations

import json
import os


class FileDownstream:
    def __init__(self, path: str):
        self.path = path
        if os.path.exists(path):
            with open(path, "r", encoding="utf-8") as fh:
                self._state = json.load(fh)
        else:
            self._state = {"applied": {}, "windows": {}}

    def _persist(self) -> None:
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(self._state, fh, indent=2, sort_keys=True)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, self.path)

    def apply(self, payload: dict) -> dict:
        """Apply a publish payload; returns the ack event for the merger."""
        result_id = payload["result_id"]
        if result_id in self._state["applied"]:
            return {"type": "ack", "result_id": result_id, "duplicate": True}
        window = payload["window"]
        delta = payload["delta"]
        self._state["applied"][result_id] = {
            "window": window,
            "kind": payload["kind"],
            "delta": delta,
        }
        ws = self._state["windows"].setdefault(window, {"total": 0, "applied": []})
        ws["total"] += delta
        ws["applied"].append(result_id)
        self._persist()
        return {"type": "ack", "result_id": result_id, "duplicate": False}

    def window_total(self, window: str):
        ws = self._state["windows"].get(window)
        return None if ws is None else ws["total"]

    def applied_ids(self) -> list[str]:
        return sorted(self._state["applied"])
