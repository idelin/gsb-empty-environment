"""Timeline crossfade engine (pure standard library, Python 3.11).

Model
-----
Segments are placed on a global output timeline at absolute sample
positions.  Where segments overlap, a crossfade of a specified length is
applied.  Fades are compiled into ONE global gain curve per segment and
summed; the total gain is checked against a constraint at every output
sample.  Fades are never applied per adjacent pair independently (which
would scale overlapping regions more than once).

Declared properties
-------------------
LATENCY_SAMPLES = 0
    The engine is a zero-latency offline renderer: a source sample's
    output position is fixed by the segment's timeline position and
    crossfades never shift time.
TOLERANCE = 1e-9
    Numeric tolerance.  Re-summing the same contributions in a different
    order (random chunked plan execution) is considered equivalent when
    the maximum absolute difference is below TOLERANCE.

Fade weight definitions (k = 0-based sample inside the fade, n = fade
length in samples):
    linear      in: k / n          out: 1 - k / n
    equal power in: sin(pi k / 2n) out: cos(pi k / 2n)
A fade-out therefore starts at full weight on its first sample and a
fade-in ends just below full weight; for a perfectly aligned pair the
linear weights sum to 1 and the equal-power powers sum to 1 at every
sample of the overlap.

Short segment rules
-------------------
A fade is only applied on a side that actually overlaps another segment.
The raw fade on each side is min(fade_len, length).  If the two fades do
not fit (fade_in + fade_out > length):
    ShortPolicy.SHORTEN (default): both fades are shortened
        proportionally, fade_in = floor(fade_in * length / total),
        fade_out = length - fade_in (fade_out takes the remainder).
    ShortPolicy.REJECT: a ShortSegmentError is raised.  REJECT also
        raises whenever a required fade is longer than the segment.

Other rules
-----------
* Empty segments (length == 0) are ignored entirely: they do not change
  the output, the mapping, or any other segment's envelope.
* Source intervals are validated up front; out-of-bounds segments are
  rejected with SourceBoundsError before any sample is read.
* Reversed segments read the source interval [start, start + length)
  backwards; the envelope is applied in output time.
* Concatenation counts the crossfade region once: total length is
  sum(lengths) - (n - 1) * fade_len.
"""

from __future__ import annotations

import enum
import math
import random
from dataclasses import dataclass, replace

LATENCY_SAMPLES = 0
TOLERANCE = 1e-9


class Curve(enum.Enum):
    LINEAR = "linear"
    EQUAL_POWER = "equal_power"


class ShortPolicy(enum.Enum):
    SHORTEN = "shorten"
    REJECT = "reject"


class XFadeError(Exception):
    """Base class for engine errors."""


class UnknownSourceError(XFadeError):
    """Segment references a source that does not exist."""


class SourceBoundsError(XFadeError):
    """Segment source interval lies outside the source array."""


class ShortSegmentError(XFadeError):
    """Segment is too short for the requested fades (REJECT policy)."""


class GainConstraintError(XFadeError):
    """Compiled total gain exceeds max_total_gain at some output sample."""


def default_max_total_gain(curve: Curve) -> float:
    """Amplitude-sum ceiling implied by the fade curve."""
    return 1.0 if curve is Curve.LINEAR else math.sqrt(2.0)


@dataclass
class Segment:
    source: str          # key into the sources mapping
    start: int           # first source sample index (inclusive)
    length: int          # number of samples; 0 => empty, ignored
    position: int        # absolute output timeline position (>= 0)
    gain: float = 1.0
    reversed: bool = False
    name: str | None = None

    @property
    def end(self) -> int:
        return self.position + self.length


@dataclass
class Envelope:
    fade_in: int
    fade_out: int
    gain: float


def fade_in_weight(curve: Curve, k: int, n: int) -> float:
    x = k / n
    return x if curve is Curve.LINEAR else math.sin(0.5 * math.pi * x)


def fade_out_weight(curve: Curve, k: int, n: int) -> float:
    x = k / n
    return (1.0 - x) if curve is Curve.LINEAR else math.cos(0.5 * math.pi * x)


def envelope_at(env: Envelope, seg_length: int, curve: Curve, t: int) -> float:
    """Envelope weight at segment-relative sample offset t."""
    w = env.gain
    if env.fade_in and t < env.fade_in:
        w *= fade_in_weight(curve, t, env.fade_in)
    if env.fade_out and t >= seg_length - env.fade_out:
        w *= fade_out_weight(curve, t - (seg_length - env.fade_out), env.fade_out)
    return w


def _active_segments(segments: list[Segment]) -> list[Segment]:
    """Non-empty segments in deterministic timeline order."""
    return sorted(
        (s for s in segments if s.length > 0),
        key=lambda s: (s.position, s.source, s.start, s.name or ""),
    )


def _validate(seg: Segment, sources) -> None:
    if seg.source not in sources:
        raise UnknownSourceError(f"unknown source {seg.source!r}")
    if seg.position < 0:
        raise SourceBoundsError(f"negative position {seg.position}")
    n = len(sources[seg.source])
    if seg.start < 0 or seg.start + seg.length > n:
        raise SourceBoundsError(
            f"segment [{seg.start}, {seg.start + seg.length}) exceeds "
            f"source {seg.source!r} of length {n}"
        )


def _compile_envelopes(
    active: list[Segment], fade_len: int, short_policy: ShortPolicy
) -> list[Envelope]:
    envs: list[Envelope] = []
    for i, seg in enumerate(active):
        fin_needed = any(
            j != i and o.position <= seg.position < o.end
            for j, o in enumerate(active)
        )
        fout_needed = any(
            j != i and o.position < seg.end <= o.end for j, o in enumerate(active)
        )
        if short_policy is ShortPolicy.REJECT:
            if (
                (fin_needed and fade_len > seg.length)
                or (fout_needed and fade_len > seg.length)
                or (fin_needed and fout_needed and 2 * fade_len > seg.length)
            ):
                raise ShortSegmentError(
                    f"segment {seg.name!r} length {seg.length} too short "
                    f"for fade_len {fade_len}"
                )
            fin = fade_len if fin_needed else 0
            fout = fade_len if fout_needed else 0
        else:
            fin = min(fade_len, seg.length) if fin_needed else 0
            fout = min(fade_len, seg.length) if fout_needed else 0
            if fin + fout > seg.length:
                total = fin + fout
                fin = fin * seg.length // total
                fout = seg.length - fin
        envs.append(Envelope(fin, fout, seg.gain))
    return envs


@dataclass
class SegMap:
    """Source-interval <-> output-interval mapping for one segment."""

    name: str | None
    source: str
    source_start: int
    source_end: int      # exclusive
    output_start: int
    output_end: int      # exclusive
    reversed: bool
    gain: float

    def source_to_output(self, s: int) -> int:
        if not self.source_start <= s < self.source_end:
            raise XFadeError(f"source sample {s} outside {self}")
        t = s - self.source_start
        if self.reversed:
            t = (self.source_end - self.source_start - 1) - t
        return self.output_start + t

    def output_to_source(self, o: int) -> int:
        if not self.output_start <= o < self.output_end:
            raise XFadeError(f"output sample {o} outside {self}")
        t = o - self.output_start
        if self.reversed:
            t = (self.output_end - self.output_start - 1) - t
        return self.source_start + t


@dataclass
class Compiled:
    segments: list[Segment]
    envelopes: list[Envelope]
    maps: list[SegMap]
    output_length: int
    peak_total_gain: float
    fade_len: int
    curve: Curve

    def output_to_sources(self, o: int) -> list[tuple[int, int, float]]:
        """(map index, source index, weight) contributions at output o."""
        if not 0 <= o < self.output_length:
            raise XFadeError(f"output sample {o} out of range")
        out = []
        for i, (seg, env) in enumerate(zip(self.segments, self.envelopes)):
            if seg.position <= o < seg.end:
                t = o - seg.position
                idx = seg.start + (seg.length - 1 - t if seg.reversed else t)
                out.append((i, idx, envelope_at(env, seg.length, self.curve, t)))
        return out


def compile_segments(
    segments: list[Segment],
    sources,
    fade_len: int,
    curve: Curve = Curve.LINEAR,
    *,
    short_policy: ShortPolicy = ShortPolicy.SHORTEN,
    max_total_gain: float | None = None,
) -> Compiled:
    """Compile the global gain curve and validate the total-gain constraint."""
    if fade_len < 0:
        raise XFadeError("fade_len must be >= 0")
    active = _active_segments(segments)
    for seg in active:
        _validate(seg, sources)
    envs = _compile_envelopes(active, fade_len, short_policy)
    out_len = max((s.end for s in active), default=0)
    if max_total_gain is None:
        max_total_gain = default_max_total_gain(curve)
    totals = [0.0] * out_len
    for seg, env in zip(active, envs):
        for t in range(seg.length):
            totals[seg.position + t] += envelope_at(env, seg.length, curve, t)
    peak = max(totals, default=0.0)
    if peak > max_total_gain + TOLERANCE:
        pos = totals.index(peak)
        raise GainConstraintError(
            f"total gain {peak:.6f} exceeds max_total_gain "
            f"{max_total_gain:.6f} at output sample {pos}"
        )
    maps = [
        SegMap(s.name, s.source, s.start, s.start + s.length,
               s.position, s.end, s.reversed, s.gain)
        for s in active
    ]
    return Compiled(active, envs, maps, out_len, peak, fade_len, curve)


def render(
    segments: list[Segment],
    sources,
    fade_len: int,
    curve: Curve = Curve.LINEAR,
    **kw,
) -> tuple[list[float], Compiled]:
    """Render the full timeline; returns (output, compiled)."""
    comp = compile_segments(segments, sources, fade_len, curve, **kw)
    out = [0.0] * comp.output_length
    for seg, env in zip(comp.segments, comp.envelopes):
        src = sources[seg.source]
        for t in range(seg.length):
            idx = seg.start + (seg.length - 1 - t if seg.reversed else t)
            out[seg.position + t] += envelope_at(env, seg.length, curve, t) * src[idx]
    return out, comp


def concatenate(specs: list[Segment], fade_len: int) -> list[Segment]:
    """Assign positions so consecutive segments overlap by exactly fade_len.

    The crossfade region is counted once: total output length is
    sum(lengths) - (n - 1) * fade_len.
    """
    out: list[Segment] = []
    pos = 0
    for s in specs:
        if pos < 0:
            raise XFadeError(
                "concatenation underflow: a segment is shorter than fade_len"
            )
        out.append(replace(s, position=pos))
        pos += s.length - fade_len
    return out


@dataclass
class MixOp:
    """One executable block: mix weights[k] * source[...] into output."""

    output_start: int
    seg_offset: int      # segment-relative offset of the first sample
    length: int
    source: str
    source_start: int
    seg_length: int
    reversed: bool
    weights: tuple


@dataclass
class Plan:
    output_length: int
    ops: list[MixOp]
    compiled: Compiled


def build_plan(
    segments: list[Segment],
    sources,
    fade_len: int,
    curve: Curve = Curve.LINEAR,
    *,
    block: int = 256,
    **kw,
) -> Plan:
    """Pre-generate an executable plan of block-sized mix operations."""
    if block <= 0:
        raise XFadeError("block must be > 0")
    comp = compile_segments(segments, sources, fade_len, curve, **kw)
    ops: list[MixOp] = []
    for seg, env in zip(comp.segments, comp.envelopes):
        t0 = 0
        while t0 < seg.length:
            n = min(block, seg.length - t0)
            weights = tuple(
                envelope_at(env, seg.length, curve, t0 + k) for k in range(n)
            )
            ops.append(
                MixOp(seg.position + t0, t0, n, seg.source, seg.start,
                      seg.length, seg.reversed, weights)
            )
            t0 += n
    return Plan(comp.output_length, ops, comp)


def execute_plan(plan: Plan, sources, seed: int | None = None) -> list[float]:
    """Execute a plan, optionally in random (chunked) order.

    Mixing is additive, so any execution order is equivalent to render()
    up to floating-point reassociation (bounded by TOLERANCE).
    """
    out = [0.0] * plan.output_length
    ops = list(plan.ops)
    if seed is not None:
        random.Random(seed).shuffle(ops)
    for op in ops:
        src = sources[op.source]
        for k in range(op.length):
            t = op.seg_offset + k
            idx = op.source_start + (
                op.seg_length - 1 - t if op.reversed else t
            )
            out[op.output_start + k] += op.weights[k] * src[idx]
    return out


@dataclass
class EditResult:
    affected_output: tuple[int, int]           # [start, end) re-rendered
    revised_sources: dict                      # name -> (source, start, end)
    output_length: int


class EditSession:
    """Incremental editor: edits re-render only the affected interval.

    Envelope/mapping metadata is recompiled (cheap, O(n)); output samples
    are re-rendered only inside the union of the old and new spans of the
    changed segments, expanded by fade_len on both sides.  Samples outside
    that interval cannot change: a segment's envelope depends only on
    overlaps at its own endpoints, and any affected fade zone lies within
    fade_len of the edit.
    """

    def __init__(
        self,
        sources,
        fade_len: int,
        curve: Curve = Curve.LINEAR,
        *,
        short_policy: ShortPolicy = ShortPolicy.SHORTEN,
        max_total_gain: float | None = None,
    ):
        self.sources = sources
        self.fade_len = fade_len
        self.curve = curve
        self.short_policy = short_policy
        self.max_total_gain = max_total_gain
        self._segments: list[Segment] = []
        self._counter = 0
        self.output: list[float] = []
        self.compiled = self._compile(self._segments)

    @property
    def segments(self) -> list[Segment]:
        return list(self._segments)

    def _compile(self, segments) -> Compiled:
        return compile_segments(
            segments,
            self.sources,
            self.fade_len,
            self.curve,
            short_policy=self.short_policy,
            max_total_gain=self.max_total_gain,
        )

    def _find(self, name: str) -> Segment:
        for s in self._segments:
            if s.name == name:
                return s
        raise XFadeError(f"no segment named {name!r}")

    def apply(self, edit) -> EditResult:
        """Apply ("add", seg) | ("remove", name) | ("replace", name, seg)
        | ("move", name, new_position).  On error the session is unchanged.
        """
        kind = edit[0]
        changed_old: list[Segment] = []
        changed_new: list[Segment] = []
        if kind == "add":
            seg = edit[1]
            if seg.name is None:
                seg = replace(seg, name=f"seg{self._counter}")
            if any(s.name == seg.name for s in self._segments):
                raise XFadeError(f"duplicate segment name {seg.name!r}")
            new_segments = self._segments + [seg]
            changed_new.append(seg)
        elif kind == "remove":
            old = self._find(edit[1])
            new_segments = [s for s in self._segments if s.name != old.name]
            changed_old.append(old)
        elif kind == "replace":
            old = self._find(edit[1])
            seg = replace(edit[2], name=old.name)
            new_segments = [seg if s.name == old.name else s
                            for s in self._segments]
            changed_old.append(old)
            changed_new.append(seg)
        elif kind == "move":
            old = self._find(edit[1])
            seg = replace(old, position=edit[2])
            new_segments = [seg if s.name == old.name else s
                            for s in self._segments]
            changed_old.append(old)
            changed_new.append(seg)
        else:
            raise XFadeError(f"unknown edit kind {kind!r}")

        comp = self._compile(new_segments)  # may raise; session untouched

        spans = [(s.position, s.end) for s in changed_old + changed_new
                 if s.length > 0]
        if spans:
            a = max(0, min(p for p, _ in spans) - self.fade_len)
            b = min(comp.output_length,
                    max(e for _, e in spans) + self.fade_len)
            a = min(a, b)  # shrunk output: affected tail was truncated
        else:
            a = b = min(comp.output_length, len(self.output))

        if comp.output_length > len(self.output):
            self.output.extend([0.0] * (comp.output_length - len(self.output)))
        elif comp.output_length < len(self.output):
            del self.output[comp.output_length:]

        for o in range(a, b):
            self.output[o] = 0.0
        for seg, env in zip(comp.segments, comp.envelopes):
            lo = max(a, seg.position)
            hi = min(b, seg.end)
            if lo >= hi:
                continue
            src = self.sources[seg.source]
            for o in range(lo, hi):
                t = o - seg.position
                idx = seg.start + (seg.length - 1 - t if seg.reversed else t)
                self.output[o] += (
                    envelope_at(env, seg.length, self.curve, t) * src[idx]
                )

        self._segments = new_segments
        if kind == "add" and edit[1].name is None:
            self._counter += 1
        self.compiled = comp
        revised = {
            s.name: (s.source, s.start, s.start + s.length)
            for s in changed_new
        }
        return EditResult((a, b), revised, comp.output_length)

    def verify(self) -> float:
        """Re-render from scratch; return max abs difference (0.0 = exact)."""
        full, _ = render(
            self._segments,
            self.sources,
            self.fade_len,
            self.curve,
            short_policy=self.short_policy,
            max_total_gain=self.max_total_gain,
        )
        if len(full) != len(self.output):
            return math.inf
        return max(
            (abs(x - y) for x, y in zip(full, self.output)), default=0.0
        )
