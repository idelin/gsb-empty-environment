"""A schema-versioned repository with atomic schema+batch publish.

publish() validates the migration and projects every affected batch
*before* touching repository state, so a failure cannot leave partial
new columns behind: the commit point is a single swap.
"""
from __future__ import annotations

from .batch import Batch
from .migration import Migration
from .schema import Schema


class RepositoryError(Exception):
    pass


class Repository:
    def __init__(self, schema: Schema):
        self._schema = schema
        self._batches = []

    @property
    def schema(self) -> Schema:
        return self._schema

    @property
    def batches(self):
        return tuple(self._batches)

    def ingest(self, batch: Batch):
        if batch.schema != self._schema:
            raise RepositoryError(
                "batch schema does not match the repository schema")
        self._batches.append(batch)

    def publish(self, migration: Migration, batch: Batch | None = None):
        """Apply a migration and optionally add a new batch, atomically.

        `batch` is expressed in the *old* schema and is projected along
        with the stored batches.  Returns the diagnostics collected while
        projecting.  On any error the repository is left untouched.
        """
        plan = migration.apply(self._schema)  # raises before any mutation
        new_batches = []
        diagnostics = []
        for old in self._batches:
            projected, diags = migration.project(self._schema, old)
            new_batches.append(projected)
            diagnostics.extend(diags)
        if batch is not None:
            projected, diags = migration.project(self._schema, batch)
            new_batches.append(projected)
            diagnostics.extend(diags)
        self._schema = plan.schema  # single atomic commit point
        self._batches = new_batches
        return diagnostics
