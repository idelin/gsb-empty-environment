"""Materialization catalog with atomic publish and disk spill.

A view becomes visible to consumers only after it has been *fully* built:

* in-memory views are built into a private list first and registered only
  on success;
* spilled views are written to a temp file and atomically renamed
  (``os.replace``) into place; the temp file is removed on failure.

If building fails at any point, the catalog contains no entry and no
partial rows are observable -- consumers never see half a table.
"""
from __future__ import annotations

import hashlib
import os
import pickle
import tempfile


class MaterializedView:
    def __init__(self, key, rows=None, path=None):
        self.key = key
        self._rows = rows
        self._path = path

    @property
    def spilled(self):
        return self._path is not None

    def read(self):
        if self._rows is not None:
            return list(self._rows)
        with open(self._path, "rb") as fh:
            return pickle.load(fh)


class Catalog:
    def __init__(self, spill_dir=None):
        self._views = {}
        self.spill_dir = spill_dir or tempfile.mkdtemp(prefix="catalog-")

    def get(self, key):
        return self._views.get(key)

    def keys(self):
        return list(self._views)

    def publish(self, key, row_iter, spill=False):
        """Build the view fully, then make it visible atomically.

        Raises whatever the producer raised; on failure the catalog and
        the spill directory are left exactly as before.
        """
        if spill:
            fd, tmp = tempfile.mkstemp(dir=self.spill_dir,
                                       prefix=".tmp-", suffix=".spill")
            try:
                with os.fdopen(fd, "wb") as fh:
                    pickle.dump(list(row_iter), fh)  # fully built first
                final = os.path.join(self.spill_dir, self._file_name(key))
                os.replace(tmp, final)  # atomic on POSIX
            except BaseException:
                if os.path.exists(tmp):
                    os.unlink(tmp)
                raise
            self._views[key] = MaterializedView(key, path=final)
        else:
            rows = list(row_iter)  # fully built BEFORE becoming visible
            self._views[key] = MaterializedView(key, rows=rows)

    @staticmethod
    def _file_name(key):
        digest = hashlib.sha1(repr(key).encode()).hexdigest()
        return f"view-{digest}.spill"
