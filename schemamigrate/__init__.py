"""schemamigrate: versioned schema evolution over typed batches.

Pure Python 3.11 standard library; no external query engine.
"""
from .batch import Batch, BatchError
from .equivalence import (
    EquivalenceReport,
    candidate_batches,
    check_path_equivalence,
    sample_values,
)
from .merge import MergeConflict, merge_schemas
from .migration import (
    AddColumn,
    AmbiguousMappingError,
    ConvertType,
    DropColumn,
    LossyConversionError,
    Migration,
    MigrationError,
    RenameColumn,
    SetNullable,
    ValueDiagnostic,
    WidenType,
    plan_migration,
    plan_renames,
)
from .repository import Repository
from .schema import ColumnDef, Schema, SchemaError, make_column, new_column_id
from .types import (
    NULL,
    RoundingPolicy,
    TypeTag,
    check_value,
    convert_value,
    is_explicit_conversion,
    is_implicit_widening,
    values_equal,
    widen_value,
)

__all__ = [
    "NULL",
    "AddColumn",
    "AmbiguousMappingError",
    "Batch",
    "BatchError",
    "ColumnDef",
    "ConvertType",
    "DropColumn",
    "EquivalenceReport",
    "LossyConversionError",
    "MergeConflict",
    "Migration",
    "MigrationError",
    "RenameColumn",
    "Repository",
    "RoundingPolicy",
    "Schema",
    "SchemaError",
    "SetNullable",
    "TypeTag",
    "ValueDiagnostic",
    "WidenType",
    "candidate_batches",
    "check_path_equivalence",
    "check_value",
    "convert_value",
    "is_explicit_conversion",
    "is_implicit_widening",
    "make_column",
    "merge_schemas",
    "new_column_id",
    "plan_migration",
    "plan_renames",
    "sample_values",
    "values_equal",
    "widen_value",
]
