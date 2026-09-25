"""secexplain: visibility-projected, replayable explanations.

A tiny deterministic forward-chaining reasoner whose explanations are
projected to the caller's clearance before anything is shown.

Security model (see README.md for the full statement):

* Low-visibility input equivalence: two complete fact sets that differ
  only in hidden parts must yield identical public explanations,
  identical public errors and identical structural counts.
* Hidden content is replaced by a single constant placeholder; the
  explanation is flagged as incomplete (this flag is the one intentional
  single-bit disclosure, required so callers know content was withheld).
* Timing side channels are explicitly OUT OF SCOPE and not promised.
"""

TIMING_SIDE_CHANNELS_IN_SCOPE = False

__all__ = [
    "model",
    "engine",
    "projection",
    "naive",
    "noninterference",
    "cache",
    "replay",
    "sampledata",
    "TIMING_SIDE_CHANNELS_IN_SCOPE",
]
