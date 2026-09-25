"""Transactional editing.

A transaction snapshots the mesh on entry. If the body raises, or if the
post-commit consistency verification (incremental state vs. full rebuild)
fails, the snapshot is restored: the original topology is always
preserved on failure and the old snapshot object is never mutated.
"""
from __future__ import annotations


class Transaction:
    def __init__(self, mesh, verify=True):
        self.mesh = mesh
        self.verify = verify
        self._snap = None

    def __enter__(self):
        self._snap = self.mesh.snapshot()
        return self.mesh

    def __exit__(self, exc_type, exc, tb):
        if exc_type is not None:
            self.mesh.restore(self._snap)
            return False
        if self.verify:
            try:
                self.mesh.verify_consistency()
            except Exception:
                self.mesh.restore(self._snap)
                raise
        return False
