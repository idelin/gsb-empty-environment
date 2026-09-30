"""Micro-batch gradient accumulator keyed by (batch_id, param_version).

Semantics:
  * Micro-batches may arrive out of order; aggregation is a sum over the
    currently active contributions, so order does not matter.
  * Re-submitting the same batch_id with identical gradients is an
    idempotent retry (returns False, nothing changes). Re-submitting with
    different gradients raises ConflictingBatchError.
  * A contribution may be withdrawn before commit; it then contributes
    nothing to the aggregate.
  * Submissions are tagged with the parameter version they were computed
    against; stale versions are rejected so gradients from before a commit
    never leak into the next accumulation round.
  * clear() empties the accumulator completely (explicit zero semantics):
    after a commit or an overflow skip, no residual gradient remains.
"""

from __future__ import annotations

from dataclasses import dataclass

from tensor import Tensor, add


class StaleVersionError(RuntimeError):
    """Submitted gradients were computed against an old parameter version."""


class ConflictingBatchError(RuntimeError):
    """Same batch_id re-submitted with different gradient content."""


@dataclass
class Contribution:
    batch_id: str
    param_version: int
    grads: dict


class GradientAccumulator:
    def __init__(self, param_version: int = 0):
        self.param_version = param_version
        self._contributions: dict[str, Contribution] = {}

    def __len__(self) -> int:
        return len(self._contributions)

    @property
    def batch_ids(self) -> set:
        return set(self._contributions)

    def submit(self, batch_id: str, grads: dict, param_version: int) -> bool:
        """Add a micro-batch contribution.

        Returns True if newly added, False if it was an idempotent retry.
        """
        if not isinstance(batch_id, str) or not batch_id:
            raise ValueError("batch_id must be a non-empty string")
        if param_version != self.param_version:
            raise StaleVersionError(
                f"batch {batch_id!r} computed against version {param_version}, "
                f"current version is {self.param_version}"
            )
        existing = self._contributions.get(batch_id)
        if existing is not None:
            if existing.grads == grads:
                return False
            raise ConflictingBatchError(
                f"batch {batch_id!r} re-submitted with different gradients"
            )
        self._contributions[batch_id] = Contribution(
            batch_id, param_version, {n: t.copy() for n, t in grads.items()}
        )
        return True

    def withdraw(self, batch_id: str) -> bool:
        """Remove a contribution; returns True if it was present."""
        return self._contributions.pop(batch_id, None) is not None

    def aggregate(self) -> dict:
        """Sum of all active contributions (empty dict if none)."""
        total = None
        for contribution in self._contributions.values():
            if total is None:
                total = {n: t.copy() for n, t in contribution.grads.items()}
            else:
                for name, tensor in contribution.grads.items():
                    total[name] = add(total[name], tensor)
        return total if total is not None else {}

    def clear(self) -> None:
        self._contributions.clear()

    def advance_version(self) -> None:
        self.param_version += 1

    def snapshot(self) -> dict:
        return {
            "param_version": self.param_version,
            "contributions": [
                {
                    "batch_id": c.batch_id,
                    "param_version": c.param_version,
                    "grads": {n: list(t.data) for n, t in c.grads.items()},
                }
                for c in self._contributions.values()
            ],
        }

    @classmethod
    def from_snapshot(cls, snap: dict) -> "GradientAccumulator":
        acc = cls(snap["param_version"])
        for c in snap["contributions"]:
            acc._contributions[c["batch_id"]] = Contribution(
                c["batch_id"],
                c["param_version"],
                {n: Tensor(v) for n, v in c["grads"].items()},
            )
        return acc
