"""All-or-nothing state commits.

A commit either fully applies (mutation + durable snapshot) or leaves the
previous state untouched. This is what binds revision publication /
application, acknowledgement and checkpoint into one atomic unit, and what
makes a re-published id a no-op instead of a double apply.
"""
from __future__ import annotations

import copy
import json
import os
import threading


class AtomicStore:
    def __init__(self, path: str | None = None, initial: dict | None = None):
        self._path = path
        self._lock = threading.RLock()
        if path and os.path.exists(path):
            with open(path, "r", encoding="utf-8") as fh:
                self._state = json.load(fh)
        else:
            self._state = copy.deepcopy(initial) if initial else {}

    @property
    def state(self) -> dict:
        return self._state

    def commit(self, mutator):
        """Run ``mutator`` against a fresh copy; swap in only on full success."""
        with self._lock:
            new_state = copy.deepcopy(self._state)
            result = mutator(new_state)
            self._persist(new_state)
            self._state = new_state
            return result

    def _persist(self, state: dict) -> None:
        if not self._path:
            return
        tmp = f"{self._path}.tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(state, fh, ensure_ascii=False, indent=2, sort_keys=True)
        os.replace(tmp, self._path)
