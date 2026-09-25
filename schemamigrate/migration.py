"""Migrations: typed steps that transform both schema and batches.

Supported version-to-version changes:
  - add a nullable column (filled with NULL)
  - drop a column
  - rename a column (identity preserved via column_id)
  - widen a type along a lossless edge (INT -> DECIMAL)
  - explicitly convert with a policy (DECIMAL -> INT), keeping per-value
    diagnostics for every lossy value

Ambiguous mappings and lossy implicit conversions are rejected at plan time.
"""
from __future__ import annotations

from dataclasses import dataclass, replace

from .batch import Batch, BatchError
from .schema import ColumnDef, Schema
from .types import (
    NULL,
    RoundingPolicy,
    TypeTag,
    convert_value,
    is_explicit_conversion,
    is_implicit_widening,
    widen_value,
)


class MigrationError(Exception):
    pass


class AmbiguousMappingError(MigrationError):
    pass


class LossyConversionError(MigrationError):
    def __init__(self, row, column, value, target_type):
        self.row = row
        self.column = column
        self.value = value
        self.target_type = target_type
        super().__init__(
            f"lossy conversion at row {row}, column {column!r}: "
            f"{value!r} -> {target_type.value} rejected by policy"
        )


@dataclass(frozen=True)
class ValueDiagnostic:
    row: int
    column: str
    original: object
    converted: object
    detail: str


def _row_count(data) -> int:
    for values in data.values():
        return len(values)
    return 0


class Step:
    """One atomic schema+data transformation.
    apply(schema, data_by_column_id) -> (new_schema, new_data, diagnostics)."""

    def apply(self, schema, data):
        raise NotImplementedError


@dataclass(frozen=True)
class AddColumn(Step):
    column: ColumnDef

    def apply(self, schema, data):
        if not self.column.nullable:
            raise MigrationError(
                f"cannot add non-nullable column {self.column.name!r}"
            )
        new_schema = Schema(schema.columns + (self.column,), version=schema.version)
        new_data = dict(data)
        new_data[self.column.column_id] = (NULL,) * _row_count(data)
        return new_schema, new_data, []


@dataclass(frozen=True)
class DropColumn(Step):
    column_id: str

    def apply(self, schema, data):
        col = schema.get(self.column_id)
        if col is None:
            raise MigrationError(f"unknown column identity {self.column_id!r}")
        new_schema = Schema(
            [c for c in schema.columns if c.column_id != self.column_id],
            version=schema.version,
        )
        new_data = dict(data)
        new_data.pop(self.column_id, None)
        return new_schema, new_data, []


@dataclass(frozen=True)
class RenameColumn(Step):
    column_id: str
    new_name: str

    def apply(self, schema, data):
        col = schema.get(self.column_id)
        if col is None:
            raise MigrationError(f"unknown column identity {self.column_id!r}")
        new_cols = [
            replace(c, name=self.new_name) if c.column_id == self.column_id else c
            for c in schema.columns
        ]
        return Schema(new_cols, version=schema.version), dict(data), []


@dataclass(frozen=True)
class SetNullable(Step):
    column_id: str
    nullable: bool

    def apply(self, schema, data):
        col = schema.get(self.column_id)
        if col is None:
            raise MigrationError(f"unknown column identity {self.column_id!r}")
        if col.nullable and not self.nullable:
            if any(v is NULL for v in data[self.column_id]):
                raise MigrationError(
                    f"column {col.name!r} contains NULL; cannot tighten nullability"
                )
        new_cols = [
            replace(c, nullable=self.nullable) if c.column_id == self.column_id else c
            for c in schema.columns
        ]
        return Schema(new_cols, version=schema.version), dict(data), []


@dataclass(frozen=True)
class WidenType(Step):
    column_id: str
    to_type: TypeTag

    def apply(self, schema, data):
        col = schema.get(self.column_id)
        if col is None:
            raise MigrationError(f"unknown column identity {self.column_id!r}")
        if not is_implicit_widening(col.type, self.to_type):
            raise MigrationError(
                f"{col.type.value} -> {self.to_type.value} is not a lossless widening"
            )
        new_cols = [
            replace(c, type=self.to_type) if c.column_id == self.column_id else c
            for c in schema.columns
        ]
        new_data = dict(data)
        new_data[self.column_id] = tuple(
            widen_value(col.type, self.to_type, v) for v in data[self.column_id]
        )
        return Schema(new_cols, version=schema.version), new_data, []


@dataclass(frozen=True)
class ConvertType(Step):
    column_id: str
    to_type: TypeTag
    policy: RoundingPolicy

    def apply(self, schema, data):
        col = schema.get(self.column_id)
        if col is None:
            raise MigrationError(f"unknown column identity {self.column_id!r}")
        if not is_explicit_conversion(col.type, self.to_type):
            raise MigrationError(
                f"no explicit conversion {col.type.value} -> {self.to_type.value}"
            )
        converted = []
        diagnostics = []
        for i, value in enumerate(data[self.column_id]):
            try:
                new_value, lossy = convert_value(col.type, self.to_type, value, self.policy)
            except ValueError as exc:
                raise LossyConversionError(i, col.name, value, self.to_type) from exc
            if lossy:
                diagnostics.append(
                    ValueDiagnostic(
                        row=i,
                        column=col.name,
                        original=value,
                        converted=new_value,
                        detail=f"{self.policy.value}: {value!r} -> {new_value!r}",
                    )
                )
            converted.append(new_value)
        new_cols = [
            replace(c, type=self.to_type) if c.column_id == self.column_id else c
            for c in schema.columns
        ]
        new_data = dict(data)
        new_data[self.column_id] = tuple(converted)
        return Schema(new_cols, version=schema.version), new_data, diagnostics


class Migration:
    """An ordered list of steps from a source schema to a target schema."""

    def __init__(self, source_schema: Schema, steps=()):
        self.source_schema = source_schema
        self.steps = tuple(steps)
        schema = source_schema
        data = {c.column_id: () for c in source_schema.columns}
        for step in self.steps:
            schema, data, _ = step.apply(schema, data)
        self.target_schema = schema

    def then(self, other):
        if self.target_schema != other.source_schema:
            raise MigrationError(
                "cannot compose: target schema of first migration "
                "!= source schema of second"
            )
        return Migration(self.source_schema, self.steps + other.steps)

    def project(self, batch: Batch):
        """Project a batch of the source schema onto the target schema.
        Returns (new_batch, diagnostics). Raises on any violation, leaving
        the state of the caller untouched."""
        if batch.schema != self.source_schema:
            raise BatchError("batch schema does not match migration source schema")
        schema = self.source_schema
        data = dict(batch._data)
        diagnostics = []
        for step in self.steps:
            schema, data, diags = step.apply(schema, data)
            diagnostics.extend(diags)
        target = self.target_schema
        out = Batch(target, {c.name: data[c.column_id] for c in target.columns})
        return out, diagnostics

    def __repr__(self):
        names = ", ".join(type(s).__name__ for s in self.steps)
        return f"Migration([{names}])"


def plan_migration(old: Schema, new: Schema, policies=None) -> Migration:
    """Derive a migration from old to new by matching column identities.

    - identities missing in new -> DropColumn
    - identities missing in old -> AddColumn (must be nullable)
    - name change -> RenameColumn
    - type change -> WidenType if lossless; ConvertType only when an explicit
      policy is supplied; otherwise rejected
    - nullability may be relaxed, never implicitly tightened
    """
    policies = policies or {}
    steps = []
    for col in old.columns:
        target = new.get(col.column_id)
        if target is None:
            steps.append(DropColumn(col.column_id))
            continue
        if target.name != col.name:
            steps.append(RenameColumn(col.column_id, target.name))
        if target.type != col.type:
            if is_implicit_widening(col.type, target.type):
                steps.append(WidenType(col.column_id, target.type))
            elif is_explicit_conversion(col.type, target.type):
                policy = policies.get(col.column_id)
                if policy is None:
                    raise MigrationError(
                        f"column {col.name!r}: {col.type.value} -> "
                        f"{target.type.value} is lossy; "
                        "an explicit policy is required"
                    )
                steps.append(ConvertType(col.column_id, target.type, policy))
            else:
                raise MigrationError(
                    f"column {col.name!r}: unsupported type change "
                    f"{col.type.value} -> {target.type.value}"
                )
        if target.nullable != col.nullable:
            if col.nullable and not target.nullable:
                raise MigrationError(
                    f"column {col.name!r}: cannot tighten nullability implicitly"
                )
            steps.append(SetNullable(col.column_id, target.nullable))
    for col in new.columns:
        if old.get(col.column_id) is None:
            steps.append(AddColumn(col))
    migration = Migration(old, steps)
    if migration.target_schema != new:
        raise MigrationError("planned migration does not reproduce the target schema")
    # keep the caller-supplied target (including its version) for projection
    migration.target_schema = new
    return migration


def plan_renames(schema: Schema, renames) -> Migration:
    """Build a rename-only migration from an old_name -> new_name mapping.
    Rejects ambiguous mappings: two source columns mapping to the same
    target name, or a target name colliding with an untouched column."""
    targets = {}
    resolved = []
    for old_name, new_name in renames.items():
        col = next((c for c in schema.columns if c.name == old_name), None)
        if col is None:
            raise MigrationError(f"unknown column {old_name!r}")
        if new_name in targets:
            raise AmbiguousMappingError(
                f"columns {targets[new_name]!r} and {old_name!r} both map "
                f"to target name {new_name!r}"
            )
        targets[new_name] = old_name
        resolved.append((col, new_name))
    for c in schema.columns:
        if c.name not in renames and c.name in targets:
            raise AmbiguousMappingError(
                f"rename target {c.name!r} collides with an existing column"
            )
    return Migration(
        schema,
        [RenameColumn(c.column_id, n) for c, n in resolved if c.name != n],
    )
