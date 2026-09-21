"""Compute breaking changes between two releases of one package.

Only public symbols can produce public breakage.  Rules:

* deleted public symbol / callable kind change -> break
* removed or newly-required parameter -> break
* parameter type narrowed -> break (input positions may only widen)
* reordered parameters -> break
* return/value type widened -> break (output positions may only narrow)
* removed behavior marker -> break
* private symbol additions/removals/changes are never public breaks
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List, Tuple

from .model import (
    KIND_CONSTANT,
    Param,
    Release,
    Symbol,
)
from .typesys import (
    SUBTYPE,
    SUPERTYPE,
    TypeLattice,
    UNKNOWN,
    UNRELATED,
)
from .version import Version, bump_kind

# Stable machine-readable reason codes.
REMOVED = "symbol-removed"
BECAME_PRIVATE = "symbol-became-private"
KIND_CHANGED = "kind-changed"
PARAM_REMOVED = "param-removed"
PARAM_REQUIRED = "param-became-required"
PARAM_NARROWED = "param-type-narrowed"
PARAM_REORDERED = "param-reordered"
PARAM_TYPE_UNKNOWN = "param-type-unknown"
RETURN_WIDENED = "return-type-widened"
RETURN_UNKNOWN = "return-type-unknown"
VALUE_WIDENED = "value-type-widened"
VALUE_UNKNOWN = "value-type-unknown"
MARKER_REMOVED = "marker-removed"


@dataclass(frozen=True)
class BreakingChange:
    package: str
    symbol: str
    reason: str
    detail: str
    old_version: str
    new_version: str
    kind_old: str
    kind_new: str


def _param_index(params: Tuple[Param, ...]) -> dict:
    return {param.name: (index, param) for index, param in enumerate(params)}


def _diff_callable(
    package: str,
    name: str,
    old: Symbol,
    new: Symbol,
    lattice: TypeLattice,
    old_version: str,
    new_version: str,
) -> List[BreakingChange]:
    changes: List[BreakingChange] = []
    old_params = _param_index(old.params)
    new_params = _param_index(new.params)

    for param_name, (_, old_param) in old_params.items():
        if param_name not in new_params:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    PARAM_REMOVED,
                    f"parameter {param_name!r} was removed",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )

    surviving_old = [param.name for param in old.params if param.name in new_params]
    surviving_new = [param.name for param in new.params if param.name in old_params]
    if surviving_old != surviving_new:
        changes.append(
            BreakingChange(
                package,
                name,
                PARAM_REORDERED,
                f"parameter order changed from ({', '.join(surviving_old)}) "
                f"to ({', '.join(surviving_new)})",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        )

    for param_name, (new_index, new_param) in new_params.items():
        old_entry = old_params.get(param_name)
        if old_entry is None:
            if new_param.required:
                changes.append(
                    BreakingChange(
                        package,
                        name,
                        PARAM_REQUIRED,
                        f"new required parameter {param_name!r} was added",
                        old_version,
                        new_version,
                        old.kind,
                        new.kind,
                    )
                )
            continue
        old_index, old_param = old_entry
        if not old_param.required and new_param.required:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    PARAM_REQUIRED,
                    f"parameter {param_name!r} became required",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )
        relation = lattice.relation(old_param.type, new_param.type)
        if relation == SUPERTYPE or relation == UNRELATED:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    PARAM_NARROWED,
                    f"parameter {param_name!r} type narrowed from "
                    f"{old_param.type} to {new_param.type}",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )
        elif relation == UNKNOWN:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    PARAM_TYPE_UNKNOWN,
                    f"parameter {param_name!r} type relationship between "
                    f"{old_param.type} and {new_param.type} is unknown",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )

    old_return = old.return_type or "object"
    new_return = new.return_type or "object"
    relation = lattice.relation(old_return, new_return)
    if relation == SUBTYPE or relation == UNRELATED:
        changes.append(
            BreakingChange(
                package,
                name,
                RETURN_WIDENED,
                f"return type widened from {old_return} to {new_return}",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        )
    elif relation == UNKNOWN:
        changes.append(
            BreakingChange(
                package,
                name,
                RETURN_UNKNOWN,
                f"return type relationship between {old_return} and "
                f"{new_return} is unknown",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        )

    removed_markers = sorted(old.markers - new.markers)
    for marker in removed_markers:
        changes.append(
            BreakingChange(
                package,
                name,
                MARKER_REMOVED,
                f"behavior marker {marker!r} was removed",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        )
    return changes


def _diff_symbol(
    package: str,
    name: str,
    old: Symbol,
    new: Symbol,
    lattice: TypeLattice,
    old_version: str,
    new_version: str,
) -> List[BreakingChange]:
    if old.is_private and new.is_private:
        return []
    if not old.is_private and new.is_private:
        return [
            BreakingChange(
                package,
                name,
                BECAME_PRIVATE,
                "public symbol was hidden as private",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        ]
    if old.is_private and not new.is_private:
        return []  # newly exposed symbol is purely additive
    if old.kind != new.kind:
        return [
            BreakingChange(
                package,
                name,
                KIND_CHANGED,
                f"symbol kind changed from {old.kind} to {new.kind}",
                old_version,
                new_version,
                old.kind,
                new.kind,
            )
        ]
    if old.kind == KIND_CONSTANT:
        changes: List[BreakingChange] = []
        old_type = old.value_type or "object"
        new_type = new.value_type or "object"
        relation = lattice.relation(old_type, new_type)
        if relation == SUBTYPE or relation == UNRELATED:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    VALUE_WIDENED,
                    f"constant value type widened from {old_type} to {new_type}",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )
        elif relation == UNKNOWN:
            changes.append(
                BreakingChange(
                    package,
                    name,
                    VALUE_UNKNOWN,
                    f"constant value type relationship between {old_type} and "
                    f"{new_type} is unknown",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )
        for marker in sorted(old.markers - new.markers):
            changes.append(
                BreakingChange(
                    package,
                    name,
                    MARKER_REMOVED,
                    f"behavior marker {marker!r} was removed",
                    old_version,
                    new_version,
                    old.kind,
                    new.kind,
                )
            )
        return changes
    return _diff_callable(
        package, name, old, new, lattice, old_version, new_version
    )


def diff_releases(
    package: str,
    old: Release,
    new: Release,
    lattice: TypeLattice,
) -> List[BreakingChange]:
    """Return all public breaking changes between two releases."""
    old_version = str(old.version)
    new_version = str(new.version)
    changes: List[BreakingChange] = []
    for name, old_symbol in sorted(old.symbols.items()):
        new_symbol = new.symbols.get(name)
        if new_symbol is None:
            if not old_symbol.is_private:
                changes.append(
                    BreakingChange(
                        package,
                        name,
                        REMOVED,
                        f"public {old_symbol.kind} was removed",
                        old_version,
                        new_version,
                        old_symbol.kind,
                        "",
                    )
                )
            continue
        changes.extend(
            _diff_symbol(
                package,
                name,
                old_symbol,
                new_symbol,
                lattice,
                old_version,
                new_version,
            )
        )
    return changes


def version_bump(old: Version, new: Version) -> str:
    return bump_kind(old, new)
