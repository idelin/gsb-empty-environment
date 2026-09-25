"""Exception types for the proofdag package."""


class ProofDAGError(Exception):
    """Base class for all proofdag errors."""


class CycleError(ProofDAGError):
    """Raised when a proof graph contains a cycle (cyclic proofs are rejected)."""


class UnknownNodeError(ProofDAGError):
    """Raised when a node references a node that does not exist."""


class ReplayError(ProofDAGError):
    """Raised when recorded evidence fails to replay deterministically."""
