"""xsplice: crossfade splicing of sample arrays on a global gain curve.

- Segments overlap freely on the output timeline; fades may re-overlap.
- The compiler builds one global gain curve and checks the total-gain
  constraint against it (no pairwise fading, no repeated rescaling).
- Equal-power and linear fade curves; explicit short-segment rule
  (shorten proportionally or reject).
- Empty segments are dropped and never change the result.
- Reversed segments (src_end < src_start) are first-class.
- Declared latency: 0 samples.  Default numeric tolerance: 1e-9.
"""

from .curves import CURVES, EQUAL_POWER, LINEAR, fade_in_gain, fade_out_gain
from .errors import (
    GainConstraintError,
    SegmentValidationError,
    ShortSegmentError,
    XSpliceError,
)
from .plan import (
    DECLARED_LATENCY,
    DEFAULT_MAX_POWER,
    DEFAULT_TOLERANCE,
    CompiledSegment,
    Interval,
    Plan,
    compile_plan,
)
from .render import render, render_chunked, render_range
from .segment import REJECT, SHORTEN, SHORT_POLICIES, Segment, resolve_fades
from .session import Revision, Session

__all__ = [
    "CURVES",
    "DECLARED_LATENCY",
    "DEFAULT_MAX_POWER",
    "DEFAULT_TOLERANCE",
    "EQUAL_POWER",
    "LINEAR",
    "REJECT",
    "SHORTEN",
    "SHORT_POLICIES",
    "CompiledSegment",
    "GainConstraintError",
    "Interval",
    "Plan",
    "Revision",
    "Segment",
    "SegmentValidationError",
    "Session",
    "ShortSegmentError",
    "XSpliceError",
    "compile_plan",
    "fade_in_gain",
    "fade_out_gain",
    "render",
    "render_chunked",
    "render_range",
    "resolve_fades",
]

__version__ = "0.1.0"
