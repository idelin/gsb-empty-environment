"""Information-loss definition for full-domain generalization.

For a categorical field with hierarchy H, generalizing a raw value to node
u costs

    loss(value -> u) = (|leaves(u)| - 1) / (|leaves(H)| - 1)

i.e. the normalized ambiguity of the generalized value: 0 for an exact
leaf, 1 for the root. Record loss is the (optionally weighted) mean over
fields; dataset loss is the mean over records. Loss is monotone
non-decreasing in each field's level, which the exact search relies on for
pruning.
"""

from __future__ import annotations

from .hierarchy import GeneralizationHierarchy


class LossModel:
    def __init__(
        self,
        hierarchies: list[GeneralizationHierarchy],
        weights: list[float] | None = None,
    ):
        self.hierarchies = hierarchies
        n = len(hierarchies)
        if weights is None:
            weights = [1.0] * n
        if len(weights) != n or any(w < 0 for w in weights) or not any(weights):
            raise ValueError("weights must be non-negative, match fields, not all zero")
        total = float(sum(weights))
        self.weights = [w / total for w in weights]
        self._denom = []
        for h in hierarchies:
            leaves = h.leaves_under(h.root)
            self._denom.append(max(leaves - 1, 1))

    def value_loss(self, field_idx: int, node: str) -> float:
        h = self.hierarchies[field_idx]
        return (h.leaves_under(node) - 1) / self._denom[field_idx]

    def dataset_loss(
        self, records: list[tuple[str, ...]], levels: tuple[int, ...]
    ) -> float:
        if not records:
            return 0.0
        total = 0.0
        for rec in records:
            rec_loss = 0.0
            for i, value in enumerate(rec):
                node = self.hierarchies[i].generalize(value, levels[i])
                rec_loss += self.weights[i] * self.value_loss(i, node)
            total += rec_loss
        return total / len(records)
