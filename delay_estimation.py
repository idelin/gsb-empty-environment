"""Sliding-window normalized cross-correlation (NCC) multi-channel delay estimation.

Design notes
------------
* Delay convention: for channels ``x`` (reference) and ``y``, a delay of ``d``
  samples means ``y[t] ~= polarity * x[t - d]`` (positive ``d`` => ``y`` lags
  ``x``).  Delays are estimated per sliding window and refined to sub-sample
  precision by parabolic interpolation of the NCC peak.
* Missing data: boolean masks mark valid samples; only positions where both
  channels are valid contribute to the correlation.
* Polarity inversion: candidates are extracted from both positive peaks and
  negative troughs of the NCC; each candidate carries a polarity of +1/-1.
* Ambiguity: every local maximum of |NCC| (including plateaus of equal height
  within ``tie_tol``) is kept as a candidate.  A window whose best candidates
  are tied is reported as non-unique; it is never silently collapsed to a
  single "best" answer.
* Trajectory selection: a Viterbi-style dynamic program selects one delay per
  window over the *whole* time axis, trading candidate evidence against a
  linear transition penalty (bounded by a hard ``max_jump`` so anomalous peaks
  cannot cause unbounded jumps).  Windows may fall back to an explicit
  "unknown" (null) state, so low-information windows stay unknown instead of
  being forced to a spurious value.
* Revision: observations can be revised after the fact (late-arriving data);
  the DP is re-run, so a correction may retroactively change the selected
  path of subsequent windows.  ``checkpoint``/``restore`` make the estimator
  state serializable; restoring and continuing is bit-identical to a
  continuous run because the DP is a deterministic function of the
  observation list.

Only the Python standard library is used.
"""

from __future__ import annotations

import itertools
import math
import pickle
from dataclasses import dataclass

# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------

#: Polarity constants.
POLARITY_POSITIVE = 1
POLARITY_NEGATIVE = -1
POLARITY_UNKNOWN = 0


@dataclass(frozen=True)
class Candidate:
    """One plausible delay for a window."""

    delay: float        # samples (fractional after refinement)
    score: float        # |NCC| at the (refined) peak, in [0, 1]
    polarity: int       # +1 or -1
    width: float        # 1-sigma peak width in samples (curvature based)


@dataclass
class WindowObservation:
    """Raw per-window evidence: the candidate set the tracker may choose from."""

    index: int                      # window ordinal
    start: int                      # sample offset of the window in x
    candidates: list                # list[Candidate], sorted by score desc
    n_overlap: int                  # best overlap (valid sample pairs) seen
    informative: bool               # enough data & energy to trust any candidate


@dataclass
class WindowEstimate:
    """Final per-window estimate along the selected trajectory."""

    index: int
    start: int
    delay: float | None             # samples; None => unknown
    delay_seconds: float | None
    polarity: int                   # 0 when unknown
    evidence: float                 # NCC score of the chosen candidate, 0 if unknown
    uncertainty: float | None       # samples; None when unknown
    unique: bool                    # False when tied candidates exist


@dataclass(frozen=True)
class TrackerParams:
    """Tunable knobs of the trajectory DP (all delays in samples)."""

    step_penalty: float = 0.10      # cost per sample of delay change between windows
    max_jump: float = 2.0           # hard bound on |delay change| between windows
    null_penalty: float = 0.5       # emission cost of declaring a window unknown
    tie_tol: float = 1e-3           # score tolerance for "tied" candidates


# ---------------------------------------------------------------------------
# Normalized cross-correlation
# ---------------------------------------------------------------------------

def window_ncc(x, y, start, length, lag, mask_x=None, mask_y=None,
               var_eps=1e-12):
    """Pearson correlation between ``x[start:start+length]`` and ``y`` shifted
    by ``lag``, restricted to positions valid in both masks.

    Returns ``(value, n_overlap)``.  ``value`` is 0.0 when the window has no
    energy (silence) in either channel; ``n_overlap`` counts valid pairs.
    """
    n = 0
    sx = sy = sxx = syy = sxy = 0.0
    end = start + length
    nx = len(x)
    ny = len(y)
    for t in range(start, end):
        if t < 0 or t >= nx:
            continue
        u = t + lag
        if u < 0 or u >= ny:
            continue
        if mask_x is not None and not mask_x[t]:
            continue
        if mask_y is not None and not mask_y[u]:
            continue
        a = x[t]
        b = y[u]
        n += 1
        sx += a
        sy += b
        sxx += a * a
        syy += b * b
        sxy += a * b
    if n == 0:
        return 0.0, 0
    var_x = n * sxx - sx * sx
    var_y = n * syy - sy * sy
    if var_x <= var_eps * n * n or var_y <= var_eps * n * n:
        # Silence (zero variance) in at least one channel: NCC undefined.
        return 0.0, n
    return (n * sxy - sx * sy) / math.sqrt(var_x * var_y), n


# ---------------------------------------------------------------------------
# Candidate extraction
# ---------------------------------------------------------------------------

def _parabolic_refine(gm, g0, gp):
    """Sub-sample peak offset from three samples of a concave-down peak."""
    denom = gm - 2.0 * g0 + gp
    if denom >= -1e-15:
        return 0.0, g0
    delta = 0.5 * (gm - gp) / denom
    delta = max(-1.0, min(1.0, delta))
    peak = g0 + 0.25 * (gp - gm) * delta
    return delta, peak


def _peak_width(gm, g0, gp):
    """Gaussian-like 1-sigma width of the peak in samples."""
    curvature = gm - 2.0 * g0 + gp          # <= 0 at a maximum
    if curvature >= -1e-15 or g0 <= 0.0:
        return float("inf")
    return math.sqrt(-2.0 * g0 / curvature)


def extract_candidates(x, y, start, length, max_lag, mask_x=None, mask_y=None,
                       min_overlap=32, min_score=0.2, tie_tol=1e-9,
                       max_candidates=8):
    """Build the candidate set for one window.

    Every local maximum of |NCC| (peaks of NCC => polarity +1, troughs =>
    polarity -1) becomes a candidate, including all members of equal-height
    plateaus.  Candidates with fewer than ``min_overlap`` valid pairs are
    discarded.  The window is marked uninformative when no candidate clears
    ``min_score`` or the window lacks energy/overlap entirely.
    """
    lags = list(range(-max_lag, max_lag + 1))
    vals = []
    best_overlap = 0
    for lag in lags:
        v, n = window_ncc(x, y, start, length, lag, mask_x, mask_y)
        vals.append((v, n))
        best_overlap = max(best_overlap, n)

    gains = [abs(v) for v, _ in vals]

    # Locate local maxima of |NCC|, merging flat plateaus into one candidate.
    raw = []  # (first_index, last_index) of each maximum/plateau
    i = 0
    m = len(lags)
    while i < m:
        g = gains[i]
        left_ok = i == 0 or g >= gains[i - 1] - tie_tol
        right_ok = i == m - 1 or g >= gains[i + 1] - tie_tol
        if left_ok and right_ok and g > 0.0:
            j = i
            while (j + 1 < m
                   and abs(gains[j + 1] - g) <= tie_tol
                   and (j + 1 == m - 1 or gains[j + 1] >= gains[j + 2] - tie_tol)):
                j += 1
            raw.append((i, j))
            i = j + 1
        else:
            i += 1

    candidates = []
    for i0, i1 in raw:
        mid = (i0 + i1) // 2
        lag = lags[mid]
        v_signed, n_overlap = vals[mid]
        if n_overlap < min_overlap:
            continue
        polarity = POLARITY_POSITIVE if v_signed >= 0.0 else POLARITY_NEGATIVE
        if i1 > i0:
            # Plateau: keep its centre, no parabolic refinement.
            centre = 0.5 * (lags[i0] + lags[i1])
            width = 0.5 * (lags[i1] - lags[i0]) + 0.5
            candidates.append(Candidate(centre, min(1.0, abs(v_signed)),
                                        polarity, width))
            continue
        g0 = gains[mid]
        if mid == 0 or mid == m - 1:
            # Boundary of the search range: one-sided parabola would be
            # biased, so keep the integer lag and mark width as unbounded.
            candidates.append(Candidate(float(lag), min(1.0, g0), polarity,
                                        float("inf")))
            continue
        gm = gains[mid - 1]
        gp = gains[mid + 1]
        delta, peak = _parabolic_refine(gm, g0, gp)
        width = _peak_width(gm, g0, gp)
        candidates.append(Candidate(lag + delta, min(1.0, max(0.0, peak)),
                                    polarity, width))

    candidates.sort(key=lambda c: c.score, reverse=True)
    if len(candidates) > max_candidates:
        # Truncate, but never split a group of tied candidates: equal-height
        # peaks must all be preserved.
        cutoff = candidates[max_candidates - 1].score
        keep = max_candidates
        while keep < len(candidates) and \
                candidates[keep].score >= cutoff - tie_tol:
            keep += 1
        del candidates[keep:]
    informative = bool(candidates) and candidates[0].score >= min_score
    return candidates, best_overlap, informative


# ---------------------------------------------------------------------------
# Trajectory selection (Viterbi DP over candidate sets)
# ---------------------------------------------------------------------------

_NULL = -1  # state index of the "unknown" null state


def _states(obs):
    """State list for a window: candidate indices plus the null state."""
    return list(range(len(obs.candidates))) + [_NULL]


def _emission(obs, state, null_penalty):
    if state == _NULL:
        return -null_penalty
    return obs.candidates[state].score


def _transition(obs_a, st_a, obs_b, st_b, params):
    if st_a == _NULL or st_b == _NULL:
        return 0.0
    da = obs_a.candidates[st_a].delay
    db = obs_b.candidates[st_b].delay
    jump = abs(db - da)
    if jump > params.max_jump + 1e-9:
        return -math.inf
    return -params.step_penalty * jump


def viterbi_track(observations, params=TrackerParams()):
    """Select one state per window maximizing total evidence minus smoothness
    cost.  Returns ``(states, objective)``."""
    n = len(observations)
    if n == 0:
        return [], 0.0
    prev = {st: _emission(observations[0], st, params.null_penalty)
            for st in _states(observations[0])}
    backpointers = []
    for k in range(1, n):
        obs = observations[k]
        back = {}
        curr = {}
        for st in _states(obs):
            best_cost = -math.inf
            best_prev = None
            for pst, pcost in prev.items():
                trans = _transition(observations[k - 1], pst, obs, st, params)
                if trans == -math.inf or pcost == -math.inf:
                    continue
                total = pcost + trans
                if total > best_cost:
                    best_cost = total
                    best_prev = pst
            curr[st] = best_cost + _emission(obs, st, params.null_penalty)
            back[st] = best_prev
        backpointers.append(back)
        prev = curr
    best_final = max(prev, key=lambda st: prev[st])
    objective = prev[best_final]
    states = [best_final]
    for k in range(n - 2, -1, -1):
        states.append(backpointers[k][states[-1]])
    states.reverse()
    return states, objective


def exhaustive_track(observations, params=TrackerParams()):
    """Brute-force reference: enumerate every state combination.  Only
    feasible for a handful of windows; used to validate the DP."""
    best_objective = -math.inf
    best_states = None
    state_lists = [_states(obs) for obs in observations]
    for combo in itertools.product(*state_lists):
        total = 0.0
        for k, st in enumerate(combo):
            total += _emission(observations[k], st, params.null_penalty)
            if k > 0:
                trans = _transition(observations[k - 1], combo[k - 1],
                                    observations[k], st, params)
                if trans == -math.inf:
                    total = -math.inf
                    break
                total += trans
        if total > best_objective:
            best_objective = total
            best_states = list(combo)
    return best_states, best_objective


# ---------------------------------------------------------------------------
# Estimator: observations in, trajectory estimates out; supports revision and
# checkpoint/restore.
# ---------------------------------------------------------------------------

class DelayEstimator:
    def __init__(self, sample_rate, params=TrackerParams()):
        self.sample_rate = float(sample_rate)
        self.params = params
        self._observations = []

    # -- observation intake -------------------------------------------------
    def add_observation(self, obs):
        if obs.index != len(self._observations):
            raise ValueError("observations must be appended in order")
        self._observations.append(obs)

    def observe(self, x, y, start, length, max_lag, mask_x=None, mask_y=None,
                **kwargs):
        """Convenience: extract candidates from the signals and append them."""
        candidates, n_overlap, informative = extract_candidates(
            x, y, start, length, max_lag, mask_x, mask_y, **kwargs)
        obs = WindowObservation(len(self._observations), start, candidates,
                                n_overlap, informative)
        self._observations.append(obs)
        return obs

    def revise_observation(self, index, obs):
        """Replace a previously recorded observation (late-arriving data).

        The trajectory is recomputed lazily on the next :meth:`estimates`
        call, so a revision may retroactively alter later windows' estimates.
        """
        if not 0 <= index < len(self._observations):
            raise IndexError(index)
        obs.index = index
        self._observations[index] = obs

    # -- trajectory ---------------------------------------------------------
    def estimates(self):
        states, _ = viterbi_track(self._observations, self.params)
        out = []
        for obs, st in zip(self._observations, states):
            out.append(self._build_estimate(obs, st))
        return out

    def _build_estimate(self, obs, st):
        if st == _NULL or not obs.informative:
            return WindowEstimate(obs.index, obs.start, None, None,
                                  POLARITY_UNKNOWN, 0.0, None, False)
        cand = obs.candidates[st]
        tied = [c for c in obs.candidates
                if c is not cand and c.score >= cand.score - self.params.tie_tol]
        unique = not tied
        uncertainty = cand.width
        if tied:
            spread = min(abs(c.delay - cand.delay) for c in tied)
            uncertainty = (uncertainty if math.isfinite(uncertainty) else 0.0) \
                + 0.5 * spread
        return WindowEstimate(
            obs.index, obs.start, cand.delay,
            cand.delay / self.sample_rate, cand.polarity, cand.score,
            uncertainty, unique)

    # -- persistence --------------------------------------------------------
    def checkpoint(self):
        """Serialize the estimator state (observations + configuration)."""
        return pickle.dumps({
            "sample_rate": self.sample_rate,
            "params": self.params,
            "observations": self._observations,
        })

    @classmethod
    def restore(cls, blob):
        state = pickle.loads(blob)
        est = cls(state["sample_rate"], state["params"])
        est._observations = list(state["observations"])
        return est
