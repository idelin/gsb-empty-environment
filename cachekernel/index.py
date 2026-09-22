"""Reverse index from dependency keys to task ids.

Keys are ("dir", id) / ("obj", id) pairs derived from evidence.  A change
set only looks up the keys it actually touched, so unrelated subtrees never
cause other tasks' traces to be re-examined.  Every lookup is recorded in
``self.accessed`` so callers (and tests) can prove index locality.
"""

from __future__ import annotations

from .evidence import dep_keys


class ReverseIndex:
    def __init__(self):
        self._map = {}        # key -> set(task_id)
        self._task_keys = {}  # task_id -> set(key)
        self.accessed = []    # keys looked up since last reset

    def add(self, evidence):
        task = evidence["task"]
        keys = dep_keys(evidence)
        self._task_keys[task] = keys
        for key in keys:
            self._map.setdefault(key, set()).add(task)

    def remove(self, task):
        """Drop every dependency of *task*; no residue may remain."""
        for key in self._task_keys.pop(task, ()):
            bucket = self._map.get(key)
            if bucket is not None:
                bucket.discard(task)
                if not bucket:
                    del self._map[key]

    def candidates(self, affected_keys):
        """Task ids whose deps intersect *affected_keys* (logs accesses)."""
        found = set()
        for key in affected_keys:
            self.accessed.append(key)
            found.update(self._map.get(key, ()))
        return found

    def reset_access_log(self):
        self.accessed = []

    def is_empty(self):
        return not self._map and not self._task_keys
