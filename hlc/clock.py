"""The hybrid logical clock state machine.

The state machine knows nothing about durability or networks: it receives
calibrated wall-clock instants and remote tags, and produces strictly
increasing local tags.  Rules enforced here:

* local/send event: tag is strictly greater than the previous local tag;
* receive event: the new local tag is strictly greater than *both* the
  previous local tag and the remote tag;
* when the physical component cannot advance but the logical counter is
  exhausted, the physical component is explicitly advanced by one
  microsecond (an overflow "leap") instead of wrapping the counter.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .tag import Tag
from .time_model import Duration, Instant

DEFAULT_MAX_LOGICAL = 0xFFFF


@dataclass
class ClockStats:
    logical_increments: int = 0
    wall_followed: int = 0
    logical_overflow_leaps: int = 0
    remote_dominated: int = 0

    def snapshot(self) -> dict:
        return {
            "logical_increments": self.logical_increments,
            "wall_followed": self.wall_followed,
            "logical_overflow_leaps": self.logical_overflow_leaps,
            "remote_dominated": self.remote_dominated,
        }


@dataclass
class HybridLogicalClock:
    node: str
    epoch: int = 0
    max_logical: int = DEFAULT_MAX_LOGICAL
    physical: int = -1
    logical: int = 0
    stats: ClockStats = field(default_factory=ClockStats)

    def start_incarnation(
        self,
        epoch: int,
        wall: Instant | None = None,
        physical_floor: int | None = None,
    ) -> None:
        """Reset volatile state for a (re)started process.

        ``physical_floor`` is the durable high-water mark recovered from the
        log: the incarnation begins strictly above it even if the wall clock
        reads an older instant.
        """
        self.epoch = int(epoch)
        floor = int(physical_floor) if physical_floor is not None else -1
        if wall is not None and wall.micros > floor:
            floor = wall.micros
        self.physical = floor
        self.logical = 0

    def _tag(self) -> Tag:
        return Tag(self.physical, self.logical, self.epoch, self.node)

    def current(self) -> Tag:
        if self.physical < 0:
            raise RuntimeError("clock incarnation not started")
        return self._tag()

    def local_tick(self, wall: Instant) -> Tag:
        """Advance for a local event or a send."""
        if wall.micros > self.physical:
            self.physical = wall.micros
            self.logical = 0
            self.stats.wall_followed += 1
        else:
            self._bump_logical()
        return self._tag()

    def receive_tick(self, wall: Instant, remote: Tag) -> Tag:
        """Advance upon consuming a remote tag.

        The result is guaranteed to be strictly greater than ``remote`` under
        the ``(physical, epoch, logical)`` ordering.
        """
        wall_p = wall.micros
        if wall_p > self.physical and wall_p > remote.physical:
            self.physical = wall_p
            self.logical = 0
            self.stats.wall_followed += 1
            return self._tag()

        # A remote tag at-or-above our physical from a newer epoch cannot be
        # dominated on the logical axis; leap one physical tick instead.
        if remote.physical >= self.physical and remote.epoch > self.epoch:
            self.physical = remote.physical + 1
            self.logical = 0
            self.stats.remote_dominated += 1
            return self._tag()

        if remote.physical > self.physical:
            self.physical = remote.physical
            if remote.epoch == self.epoch:
                self.logical = remote.logical + 1
            else:
                # Our newer epoch dominates at the same physical value.
                self.logical = 0
            self.stats.remote_dominated += 1
        elif remote.physical == self.physical:
            if remote.epoch == self.epoch:
                self.logical = max(self.logical, remote.logical) + 1
            else:
                # Older remote epoch: only our own previous tag matters.
                self.logical += 1
            self.stats.remote_dominated += 1
        else:
            self.logical += 1
        self.stats.logical_increments += 1
        if self.logical > self.max_logical:
            self._overflow_leap()
        return self._tag()

    def _bump_logical(self) -> None:
        self.logical += 1
        self.stats.logical_increments += 1
        if self.logical > self.max_logical:
            self._overflow_leap()

    def _overflow_leap(self) -> None:
        """Explicit counter-overflow policy: advance physical by one tick."""
        self.physical += 1
        self.logical = 0
        self.stats.logical_overflow_leaps += 1


def is_far_future(
    remote: Tag,
    wall: Instant,
    local: Tag | None,
    allowed_skew: Duration,
) -> bool:
    """Return True when a remote tag lies beyond the explicit skew bound.

    The bound is measured from the greater of the current wall reading and
    the local physical high-water mark, so a process running slightly ahead
    of wall time is not falsely accused.
    """
    reference = wall.micros
    if local is not None and local.physical > reference:
        reference = local.physical
    return remote.physical > reference + allowed_skew.micros
