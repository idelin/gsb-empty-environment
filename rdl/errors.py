"""Typed errors for the reliable delivery layer."""


class RDLError(Exception):
    """Base class for every RDL error."""


class PartitionNotFoundError(RDLError):
    def __init__(self, partition: str):
        super().__init__(f"unknown partition: {partition!r}")
        self.partition = partition


class PartitionFrozenError(RDLError):
    """Partition is fenced / frozen (e.g. exported away from this owner)."""

    def __init__(self, partition: str, owner: str | None = None):
        msg = f"partition {partition!r} is frozen"
        if owner is not None:
            msg += f" (owner={owner!r})"
        super().__init__(msg)
        self.partition = partition
        self.owner = owner


class CapacityExceededError(RDLError):
    """Rejecting an ingress: live slot count is already at the hard cap."""

    def __init__(self, partition: str, live: int, cap: int, tier: str):
        super().__init__(
            f"partition {partition!r} capacity exceeded: live={live} cap={cap} tier={tier}"
        )
        self.partition = partition
        self.live = live
        self.cap = cap
        self.tier = tier


class SpillFailureError(RDLError):
    """Spool flush to disk failed while admitting an ingress."""

    def __init__(self, partition: str, abs_index: int, reason: str):
        super().__init__(
            f"partition {partition!r} spill failed at abs={abs_index}: {reason}"
        )
        self.partition = partition
        self.abs_index = abs_index
        self.reason = reason


class StaleEpochError(RDLError):
    """Ingress belongs to an epoch older than the anchored source epoch."""

    def __init__(self, partition: str, epoch: int, anchored: int | None):
        super().__init__(
            f"partition {partition!r} stale epoch {epoch} (anchored={anchored})"
        )
        self.partition = partition
        self.epoch = epoch
        self.anchored = anchored


class ProofRejectedError(RDLError):
    def __init__(self, partition: str, reason: str):
        super().__init__(f"partition {partition!r} proof rejected: {reason}")
        self.partition = partition
        self.reason = reason


class InvalidDeliveryIDError(RDLError):
    def __init__(self, delivery_id: str, reason: str = ""):
        super().__init__(f"invalid delivery id {delivery_id!r}: {reason}")
        self.delivery_id = delivery_id


class MigrationError(RDLError):
    pass
