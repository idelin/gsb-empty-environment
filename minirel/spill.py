"""Spill partition management with guaranteed cleanup."""

import os
import pickle
import tempfile


class SpillManager:
    """Owns the spill directory and every partition file.

    cleanup() removes all partition files (and the directory if we created
    it); it is idempotent and must run on both success and failure paths so
    that no temporary state can be mistaken for a complete result.
    """

    def __init__(self, spill_dir=None, open_fn=open):
        self._dir = spill_dir
        self._owns_dir = spill_dir is None
        self._made = False
        self._open_fn = open_fn
        self.files = []

    @property
    def dir(self):
        if not self._made:
            if self._owns_dir:
                self._dir = tempfile.mkdtemp(prefix="minirel-spill-")
            else:
                os.makedirs(self._dir, exist_ok=True)
            self._made = True
        return self._dir

    def spill(self, obj):
        """Serialize obj into a new partition file; return its path."""
        path = os.path.join(self.dir, f"part-{len(self.files)}.pkl")
        try:
            fobj = self._open_fn(path, "wb")
            try:
                pickle.dump(obj, fobj)
            finally:
                fobj.close()
        except BaseException:
            # A half-written partition must never survive.
            if os.path.exists(path):
                os.remove(path)
            raise
        self.files.append(path)
        return path

    def load(self, path):
        with self._open_fn(path, "rb") as fobj:
            return pickle.load(fobj)

    def cleanup(self):
        for path in self.files:
            try:
                os.remove(path)
            except FileNotFoundError:
                pass
        self.files.clear()
        if self._owns_dir and self._made:
            try:
                os.rmdir(self._dir)
            except OSError:
                pass
