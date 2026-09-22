"""Exception hierarchy for the plan DAG library.

Nothing in this package executes SQL or evaluates embedded code; every
failure mode below is raised while parsing, validating, binding, or running
an explicitly allow-listed set of plan operators and expressions.
"""


class PlanDagError(Exception):
    """Base class for all errors raised by this package."""


class DocumentFormatError(PlanDagError):
    """The serialized document is not a well-formed plan document."""


class UnsupportedVersionError(PlanDagError):
    """The document or node carries a type/version this build cannot read."""


class UnknownOperatorError(PlanDagError):
    """An operator tag is not present in the static operator registry."""


class MissingNodeError(PlanDagError):
    """An edge (or the root) refers to a node id that does not exist."""


class CycleError(PlanDagError):
    """The node graph contains a directed cycle."""


class NodeConflictError(PlanDagError):
    """One node id is used for two nodes with different payloads."""


class BindingError(PlanDagError):
    """Base class for errors found while binding a plan before execution."""


class UnknownFieldError(BindingError):
    """An expression references a column that is not in scope."""


class AmbiguousFieldError(BindingError):
    """A column name resolves to more than one input column."""


class TypeMismatchError(BindingError):
    """Declared/inferred types do not line up; found before execution."""


class UnknownTableError(BindingError):
    """A scan references a table that is not in the local catalog."""


class ExecutionError(PlanDagError):
    """A runtime error encountered while evaluating a bound plan."""
