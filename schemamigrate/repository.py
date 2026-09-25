"""A repository that publishes schema + batches atomically.

publish() validates and projects every batch in memory first; only when
all of them succeed does it swap schema and batches in one step. Any error
aborts the publish and leaves the previous state fully intact -- no
partially added columns, no half-migrated batches.
"""
from __future__ import annotations

from .batch import BatchError
from .migration import MigrationError


class Repository:
    def __init__(self, schema):
        self._schema = schema
        self._batches = []

    @property
    def schema(self):
        return self._schema

    @property
    def batches(self):
        return tuple(self._batches)

    def append(self, batch):
        if batch.schema != self._schema:
            raise BatchError("batch schema does not match repository schema")
        self._batches.append(batch)

    def publish(self, migration, batches=None):
        """Atomically move the repository to migration.target_schema.

        Migrates the stored batches (or the explicitly supplied ones) and
        commits schema + projected batches together. Returns the collected
        per-value diagnostics from explicit lossy conversions.
        """
        if migration.source_schema != self._schema:
            raise MigrationError(
                "migration source schema does not match repository schema"
            )
        source_batches = list(self._batches) if batches is None else list(batches)
        projected = []
        diagnostics = []
        for batch in source_batches:
            new_batch, diags = migration.project(batch)
            projected.append(new_batch)
            diagnostics.extend(diags)
        self._schema = migration.target_schema
        self._batches = projected
        return diagnostics
