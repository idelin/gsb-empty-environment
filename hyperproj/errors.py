"""Exception hierarchy for projection batch validation."""


class ProjectionError(Exception):
    """Base class for every rejected projection batch."""


class IdConflictError(ProjectionError):
    """A vertex/group/edge id already exists in its namespace."""


class UnknownIdError(ProjectionError):
    """A referenced vertex, edge or group id does not exist."""


class HierarchyCycleError(ProjectionError):
    """The operation would introduce a cycle into the group forest."""


class InvalidIntervalError(ProjectionError):
    """Edge time interval is empty or inverted (must satisfy start < end)."""
