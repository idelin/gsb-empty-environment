"""Cyclic sequence / epoch coordinate math.

Wire sequence numbers wrap at ``modulus``.  Internally every event is mapped to
a non-cyclical *absolute index* (``abs``).  The mapping is anchored at the
frontier:

* when the next expected position ``expected_abs`` is known, a wire seq maps to
  either ``expected_abs + fwd`` (future/expected lap) or
  ``expected_abs - back`` (previous lap);
* the candidate inside the unambiguous reorder window wins.

Epoch transitions do not reset the absolute index - a source failover continues
the same stream.  ``EpochAnchor`` records, for every observed epoch, the abs at
which that epoch began (estimated from the first ingress), so events from old
and new epochs can be compared even after several wrap-arounds.
"""

from __future__ import annotations

from dataclasses import dataclass


def fwd_distance(seq: int, base_seq: int, modulus: int) -> int:
    """Forward cyclic distance ``base_seq -> seq`` in ``[0, modulus)``."""
    return (seq - base_seq) % modulus


@dataclass
class Position:
    kind: str                       # future | past
    abs_index: int
    distance: int                   # abs(abs - expected_abs)
    forward: bool                   # True if seq belongs to the upcoming lap


@dataclass
class EpochAnchor:
    """abs at which an epoch first appeared.

    ``first_abs`` / ``first_seq`` describe the first accepted ingress of the
    epoch.  ``start_abs`` is the estimated abs of the epoch's first wire seq
    (seq 1 by convention), used only for epoch/abs diagnostics.
    """

    epoch: int
    first_abs: int
    first_seq: int
    start_abs: int

    @classmethod
    def from_ingress(cls, epoch: int, abs_index: int, seq: int) -> "EpochAnchor":
        # seq n corresponds to abs (start_abs + n - 1); invert it.
        start_abs = abs_index - (seq - 1)
        return cls(epoch=epoch, first_abs=abs_index, first_seq=seq, start_abs=start_abs)


class SequenceTracker:
    """Stateful mapper between (epoch, wire seq) and absolute indices."""

    def __init__(
        self,
        modulus: int,
        initial_seq: int = 1,
        initial_abs: int = 0,
        reorder_window: int | None = None,
    ):
        self.modulus = modulus
        self.initial_seq = initial_seq
        self.initial_abs = initial_abs
        self.window = (modulus // 2 - 1) if reorder_window is None else reorder_window
        # absolute index of the next event the stream *will* produce
        self.next_abs = initial_abs
        # wire seq corresponding to ``next_abs``
        self.next_seq = initial_seq % modulus
        # epoch expected at the frontier
        self.frontier_epoch: int | None = None
        self.anchors: dict[int, EpochAnchor] = {}

    # -- (de)serialization -------------------------------------------------

    def to_dict(self) -> dict:
        return {
            "modulus": self.modulus,
            "initial_seq": self.initial_seq,
            "initial_abs": self.initial_abs,
            "window": self.window,
            "next_abs": self.next_abs,
            "next_seq": self.next_seq,
            "frontier_epoch": self.frontier_epoch,
            "anchors": {
                str(e): vars(a) for e, a in sorted(self.anchors.items())
            },
        }

    @classmethod
    def from_dict(cls, data: dict) -> "SequenceTracker":
        st = cls(
            modulus=data["modulus"],
            initial_seq=data.get("initial_seq", 1),
            initial_abs=data.get("initial_abs", 0),
            reorder_window=data.get("window"),
        )
        st.next_abs = data["next_abs"]
        st.next_seq = data["next_seq"]
        st.frontier_epoch = data.get("frontier_epoch")
        st.anchors = {
            int(k): EpochAnchor(**v) for k, v in data.get("anchors", {}).items()
        }
        return st

    # -- core mapping ------------------------------------------------------

    def advance_to(self, abs_index: int, seq: int, epoch: int | None = None) -> None:
        """Move the frontier past ``abs_index`` (the slot became contiguous)."""
        self.next_abs = abs_index + 1
        self.next_seq = (seq + 1) % self.modulus
        if epoch is not None:
            self.frontier_epoch = epoch

    def anchor_epoch(self, epoch: int, abs_index: int, seq: int) -> EpochAnchor:
        existing = self.anchors.get(epoch)
        if existing is None:
            existing = EpochAnchor.from_ingress(epoch, abs_index, seq)
            self.anchors[epoch] = existing
        return existing

    def resolve(self, seq: int) -> Position:
        """Map a wire seq to an abs, given the current frontier.

        Returns the candidate with the smaller cyclic distance.  Ties cannot
        happen for modulus >= 2 and a window < modulus/2.
        """
        fwd = fwd_distance(seq, self.next_seq, self.modulus)
        if fwd == 0:
            return Position("future", self.next_abs, 0, True)
        back = self.modulus - fwd
        if fwd <= back:
            return Position("future", self.next_abs + fwd, fwd, True)
        return Position("past", self.next_abs - back, back, False)

    def within_window(self, pos: Position) -> bool:
        return pos.distance <= self.window

    def seq_of_abs(self, abs_index: int) -> int:
        delta = abs_index - self.initial_abs
        return (self.initial_seq + delta) % self.modulus

    def expected_epoch(self, pos: Position) -> int | None:
        """Best guess for the epoch that should occupy ``pos``."""
        if self.frontier_epoch is None:
            return None
        # Epoch anchors tell us where epochs begin.  An abs belongs to the
        # highest anchor with start_abs <= abs.
        best = self.frontier_epoch
        for epoch, anchor in self.anchors.items():
            if anchor.start_abs <= pos.abs_index and epoch > best:
                best = epoch
        return best
