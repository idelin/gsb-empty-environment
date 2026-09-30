"""Exception types for the pseudonymizer package."""


class PseudonymError(Exception):
    """Base class for all pseudonymizer errors."""


class OperationCancelled(PseudonymError):
    """Raised when an operation is cancelled via its cancel event.

    The store is guaranteed to be unchanged when this is raised.
    """


class UnknownPseudonym(PseudonymError):
    """Raised when referencing a pseudonym that does not exist."""


class DuplicateKeyVersion(PseudonymError):
    """Raised when registering a key version that already exists."""


class ConsistencyError(PseudonymError):
    """Raised when a consistency / recovery check finds violations."""

    def __init__(self, problems):
        self.problems = list(problems)
        super().__init__(
            "consistency check failed: " + "; ".join(self.problems)
        )
