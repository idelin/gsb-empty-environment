"""Schema migrations and batch projection.

Supported version-mode ops:
  AddColumn     - new column, must be nullable
  DropColumn    - remove a column by identity
  RenameColumn  - relabel a column; identity (cid) is preserved
  WidenColumn   - implicit, exact widening only (int -> decimal)
  ConvertColumn - explicit conversion; lossy ones need a policy and keep
                  per-value diagnostics

Validation rejects ambiguous mappings (two source columns landing on the
same target name) and lossy implicit conversions.
"""
from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any, Optional

from .batch import Batch
from .schema import Column, Schema
from .typesys import (
    Policy, Type, Diagnostic, check_value, convert_value,
    is_exact_widening, is_explicit_conversion, is_lossy,
)


class MigrationError(Exception):
    pass


class AmbiguousMappingError(MigrationError):
    """Two source columns map to the same target name."""


class LossyImplicitError(MigrationError):
    """A lossy conversion was requested without an explicit policy."""


@dataclass(frozen=True)
class AddColumn:
    cid: str
    name: str
    type: Type
    nullable: bool = True
    default: Any = None


@dataclass(frozen=True)
class DropColumn:
    cid: str


@dataclass(frozen=True)
class RenameColumn:
    cid: str
    new_name: str


@dataclass(frozen=True)
class WidenColumn:
    cid: str
    new_type: Type


@dataclass(frozen=True)
class ConvertColumn:
    cid: str
    new_type: Type
    policy: Optional[Policy] = None


@dataclass(frozen=True)
class _Target:
    column: Column
    source: Optional[Column]      # None for added columns
    steps: tuple                  # (new_type, policy) conversion chain
    default: Any


class Plan:
    def __init__(self, schema: Schema, targets):
        self.schema = schema
        self.targets = tuple(targets)


class Migration:
    def __init__(self, ops=()):
        self.ops = tuple(ops)

    def __add__(self, other: "Migration") -> "Migration":
        return Migration(self.ops + other.ops)

    def apply(self, schema: Schema) -> Plan:
        state = {c.cid: c for c in schema.columns}
        order = [c.cid for c in schema.columns]
        steps = {cid: [] for cid in order}
        defaults = {}
        for op in self.ops:
            if isinstance(op, AddColumn):
                if not op.nullable:
                    raise MigrationError("new columns must be nullable")
                if op.cid in state:
                    raise MigrationError(f"column identity {op.cid!r} already exists")
                if op.default is not None and not check_value(op.type, op.default):
                    raise MigrationError(
                        f"default {op.default!r} does not match type {op.type.value}")
                state[op.cid] = Column(op.cid, op.name, op.type, op.nullable)
                order.append(op.cid)
                steps[op.cid] = None
                defaults[op.cid] = op.default
            elif isinstance(op, DropColumn):
                self._get(state, op.cid)
                del state[op.cid]
                order.remove(op.cid)
                del steps[op.cid]
            elif isinstance(op, RenameColumn):
                col = self._get(state, op.cid)
                state[op.cid] = replace(col, name=op.new_name)
            elif isinstance(op, WidenColumn):
                col = self._get(state, op.cid)
                if col.type is op.new_type:
                    raise MigrationError(f"no-op widening on column {col.name!r}")
                if not is_exact_widening(col.type, op.new_type):
                    raise LossyImplicitError(
                        f"{col.type.value}->{op.new_type.value} is not an exact "
                        f"widening; use ConvertColumn with an explicit policy")
                state[op.cid] = replace(col, type=op.new_type)
                steps[op.cid].append((op.new_type, None))
            elif isinstance(op, ConvertColumn):
                col = self._get(state, op.cid)
                if col.type is op.new_type:
                    raise MigrationError(f"no-op conversion on column {col.name!r}")
                if not (is_exact_widening(col.type, op.new_type)
                        or is_explicit_conversion(col.type, op.new_type)):
                    raise MigrationError(
                        f"unsupported conversion "
                        f"{col.type.value}->{op.new_type.value}")
                if is_lossy(col.type, op.new_type) and op.policy is None:
                    raise LossyImplicitError(
                        f"{col.type.value}->{op.new_type.value} is lossy and "
                        f"requires an explicit policy")
                state[op.cid] = replace(col, type=op.new_type)
                steps[op.cid].append((op.new_type, op.policy))
            else:
                raise MigrationError(f"unknown migration op {op!r}")
        names = [state[cid].name for cid in order]
        dupes = sorted({n for n in names if names.count(n) > 1})
        if dupes:
            raise AmbiguousMappingError(
                f"ambiguous mapping: multiple source columns land on {dupes}")
        targets = []
        for cid in order:
            col = state[cid]
            chain = steps[cid]
            if chain is None:
                targets.append(_Target(col, None, (), defaults[cid]))
            else:
                targets.append(
                    _Target(col, schema.column(cid), tuple(chain), None))
        return Plan(Schema(state[cid] for cid in order), targets)

    def project(self, schema: Schema, batch: Batch):
        """Project a batch of the source schema onto the target schema.

        Returns (new_batch, diagnostics).  Pure: no partial output is
        produced on error, the exception propagates instead.
        """
        if batch.schema != schema:
            raise MigrationError("batch does not match the source schema")
        plan = self.apply(schema)
        diagnostics = []
        columns = {}
        for target in plan.targets:
            if target.source is None:
                columns[target.column.cid] = tuple(
                    target.default for _ in range(batch.nrows))
                continue
            out = []
            for row, value in enumerate(batch.column(target.source.cid)):
                current = value
                ctype = target.source.type
                for new_type, policy in target.steps:
                    current = convert_value(
                        current, ctype, new_type, policy,
                        target.column.name, row, diagnostics)
                    ctype = new_type
                out.append(current)
            if not target.column.nullable and any(v is None for v in out):
                raise MigrationError(
                    f"nulls in non-nullable target column {target.column.name!r}")
            columns[target.column.cid] = tuple(out)
        return Batch(plan.schema, columns), diagnostics

    @staticmethod
    def _get(state, cid):
        try:
            return state[cid]
        except KeyError:
            raise MigrationError(f"unknown column identity {cid!r}") from None
