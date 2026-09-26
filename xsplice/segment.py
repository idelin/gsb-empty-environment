"""Segment model, validation and the short-segment rule."""

from __future__ import annotations

from dataclasses import dataclass

from .curves import CURVES, LINEAR
from .errors import SegmentValidationError, ShortSegmentError

SHORTEN = "shorten"
REJECT = "reject"
SHORT_POLICIES = (SHORTEN, REJECT)


@dataclass(frozen=True)
class Segment:
    """A slice of a source buffer placed on the output timeline.

    ``src_end`` is exclusive.  If ``src_end < src_start`` the segment plays
    its source range in reverse.  ``src_start == src_end`` is an empty
    segment: it is dropped at compile time and never changes the result.
    All positions and lengths are integer sample counts.
    """

    seg_id: str
    src_id: str
    src_start: int
    src_end: int
    offset: int
    fade_in: int = 0
    fade_out: int = 0
    curve: str = LINEAR

    @property
    def duration(self) -> int:
        return abs(self.src_end - self.src_start)

    @property
    def is_empty(self) -> bool:
        return self.src_end == self.src_start

    @property
    def step(self) -> int:
        return 1 if self.src_end > self.src_start else -1

    @property
    def end(self) -> int:
        return self.offset + self.duration

    @property
    def src_lo(self) -> int:
        return min(self.src_start, self.src_end)

    @property
    def src_hi(self) -> int:
        return max(self.src_start, self.src_end)


def validate_segment(seg: Segment, src_len: int) -> None:
    """Reject malformed segments; guarantees no out-of-bounds source reads."""
    for name in ("src_start", "src_end", "offset", "fade_in", "fade_out"):
        value = getattr(seg, name)
        if not isinstance(value, int) or isinstance(value, bool):
            raise SegmentValidationError(
                f"{seg.seg_id!r}: {name} must be an int, got {value!r}"
            )
    if seg.offset < 0:
        raise SegmentValidationError(
            f"{seg.seg_id!r}: offset must be >= 0, got {seg.offset}"
        )
    if seg.fade_in < 0 or seg.fade_out < 0:
        raise SegmentValidationError(
            f"{seg.seg_id!r}: fades must be >= 0, "
            f"got fade_in={seg.fade_in}, fade_out={seg.fade_out}"
        )
    if seg.curve not in CURVES:
        raise SegmentValidationError(
            f"{seg.seg_id!r}: unknown curve {seg.curve!r}, expected one of {CURVES}"
        )
    if not (0 <= seg.src_lo and seg.src_hi <= src_len):
        raise SegmentValidationError(
            f"{seg.seg_id!r}: source range [{seg.src_lo}, {seg.src_hi}) "
            f"outside source {seg.src_id!r} of length {src_len}"
        )


def resolve_fades(seg: Segment, policy: str = SHORTEN) -> tuple[int, int]:
    """Effective ``(fade_in, fade_out)`` after the short-segment rule.

    Rule: fades are valid iff ``fade_in + fade_out <= duration``.  When the
    segment is too short the policy decides:

    - ``SHORTEN``: scale both fades down proportionally (ratio preserved,
      floored to whole samples) so their sum fits inside the segment;
    - ``REJECT``: raise :class:`ShortSegmentError`.

    The rule is total: effective fades never extend past the segment, so no
    gain is ever evaluated outside the segment's own range.
    """
    fade_in, fade_out = seg.fade_in, seg.fade_out
    duration = seg.duration
    if fade_in + fade_out <= duration:
        return fade_in, fade_out
    if policy == REJECT:
        raise ShortSegmentError(
            f"{seg.seg_id!r}: fade_in + fade_out = {fade_in + fade_out} "
            f"exceeds duration {duration}"
        )
    if policy != SHORTEN:
        raise ValueError(
            f"unknown short-segment policy {policy!r}, expected one of {SHORT_POLICIES}"
        )
    if duration == 0:
        return 0, 0
    scale = duration / (fade_in + fade_out)
    return int(fade_in * scale), int(fade_out * scale)
