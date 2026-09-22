"""Error hierarchy for documents, plans, and execution."""


class PlanError(Exception):
    """Base class for every error raised by this package."""


class DocumentError(PlanError):
    """Base class for serialization / deserialization errors."""


class FormatVersionError(DocumentError):
    """Document format version is missing or unsupported."""


class UnknownOperatorError(DocumentError):
    """Document references an operator or expression type that is not registered."""


class InvalidDocumentError(DocumentError):
    """Document structure is malformed (bad types, unknown fields, ...)."""


class PlanValidationError(PlanError):
    """Base class for semantic validation failures."""


class CycleError(PlanValidationError):
    """Plan graph contains a cycle."""


class MissingNodeError(PlanValidationError):
    """An input reference points at a node id that does not exist."""


class DuplicateNodeError(DocumentError):
    """Same node id appears twice with different content, or twice in one document."""


class BindingError(PlanValidationError):
    """Field / type binding failed before execution (missing columns, bad types, ...)."""


class AmbiguousColumnError(BindingError):
    """Unqualified column name matches more than one input field."""


class UnknownTableError(BindingError):
    """Scan references a table that is not in the local catalog."""


class PlanExecutionError(PlanError):
    """Runtime failure that survives pre-execution validation (e.g. division by zero)."""
