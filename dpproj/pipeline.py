"""Release pipeline: bounded-contribution counts -> seeded Laplace noise ->
exact rational QP projection with KKT evidence.

Key rules implemented here (see README for full definitions):

* Public fixed buckets only; counts are synthetic in this repo.
* The total target must come from an explicit source: either a public
  constant (`PublicTarget`) or a separately noised value (`NoisedTarget`)
  with its own ledger key and its own epsilon share. The noisy bucket
  values themselves are never reused to derive the target.
* Noise is drawn once per (release, bucket) key and reused on repeat
  releases -- re-running a release never re-draws noise.
* The projection is a deterministic function of the noisy input; it never
  resamples. Zero buckets and negative noisy values are handled by the
  non-negativity constraint of the QP.
* An infeasible total (e.g. a negative target) raises InfeasibleError --
  the release is explicitly rejected. If the iteration budget is
  exhausted, the result is marked converged=False / "not_converged".
* The noisy (pre-projection) values and the post-processed projection are
  kept in separate fields of the result.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Optional, Sequence, Union

from .noise import NoiseLedger
from .qp import (InfeasibleError, KKTCertificate, QPResult, solve_qp,
                 verify_kkt)


@dataclass(frozen=True)
class PublicTarget:
    """A total/constraint target that is public knowledge (no noise)."""
    value: Fraction


@dataclass(frozen=True)
class NoisedTarget:
    """A total/constraint target noised with its own epsilon share.

    The draw uses its own ledger key, independent of the bucket noise."""
    epsilon: Fraction


TargetSource = Union[PublicTarget, NoisedTarget]


@dataclass(frozen=True)
class ConsistencyConstraint:
    """One linear consistency row: sum_i coeffs[i] * x[i] = target.

    `coeffs` maps bucket name -> rational coefficient. Overlapping bucket
    groups are expressed by giving each group its own constraint row."""
    coeffs: dict[str, Fraction]
    target: TargetSource
    name: str = ""


@dataclass(frozen=True)
class ReleaseConfig:
    release_id: str
    buckets: tuple[str, ...]           # public, fixed bucket definitions
    epsilon: Fraction                  # epsilon for the bucket counts
    l1_sensitivity: Fraction           # bounded-contribution L1 sensitivity
    total: TargetSource                # mandatory explicit total source
    constraints: tuple[ConsistencyConstraint, ...] = ()
    max_iterations: int = 1000


@dataclass
class ReleaseResult:
    release_id: str
    status: str                        # "ok" | "not_converged"
    converged: bool
    iterations: int
    noisy_counts: dict[str, float]     # raw noised values (pre-projection)
    projected: dict[str, Fraction]     # post-processed, non-negative
    total_target: Fraction
    objective: Optional[Fraction]
    kkt: Optional[KKTCertificate]
    params: dict = field(default_factory=dict)


def _resolve_target(ledger: NoiseLedger, key: str,
                    source: TargetSource, true_value: Fraction,
                    l1_sensitivity: Fraction) -> tuple[Fraction, bool]:
    """Resolve a target source to a rational value.

    `true_value` is the (sensitive) true answer of the corresponding
    linear query. A PublicTarget ignores it and uses the public constant;
    a NoisedTarget releases true_value + Laplace(sensitivity / epsilon)
    with its own ledger key and epsilon share. Returns (value, was_noised).
    A noised float target is rounded to a rational with denominator 10^6
    so downstream exact arithmetic stays manageable; the rounding is
    deterministic and part of the public spec.
    """
    if isinstance(source, PublicTarget):
        return Fraction(source.value), False
    scale = l1_sensitivity / source.epsilon
    draw = ledger.laplace(key, scale)
    return (Fraction(true_value) + Fraction(draw)).limit_denominator(
        1_000_000), True


def run_release(config: ReleaseConfig, ledger: NoiseLedger,
                counts: dict[str, int]) -> ReleaseResult:
    """Run one release: noise (draw-once) then deterministic projection."""
    buckets = list(config.buckets)
    if set(counts) != set(buckets):
        raise ValueError("counts must cover exactly the public buckets")
    if any(c < 0 for c in counts.values()):
        raise ValueError("counts must be non-negative")
    eps = Fraction(config.epsilon)
    sens = Fraction(config.l1_sensitivity)
    if eps <= 0 or sens <= 0:
        raise ValueError("epsilon and sensitivity must be positive")

    # ---- Step 1: noise (draw-once via the ledger) -----------------------
    scale = sens / eps
    noisy = {
        name: counts[name] + ledger.laplace(
            f"{config.release_id}:bucket:{name}", scale)
        for name in buckets
    }

    # ---- Step 2: build the constraint system ----------------------------
    a_rows: list[list[Fraction]] = []
    b_vals: list[Fraction] = []
    true_total = Fraction(sum(counts.values()))
    total_value, _ = _resolve_target(
        ledger, f"{config.release_id}:total", config.total,
        true_total, sens)
    a_rows.append([Fraction(1)] * len(buckets))
    b_vals.append(total_value)
    for con in config.constraints:
        row = [Fraction(con.coeffs.get(name, 0)) for name in buckets]
        true_value = sum(Fraction(con.coeffs.get(name, 0))
                         * counts[name] for name in buckets)
        value, _ = _resolve_target(
            ledger, f"{config.release_id}:constraint:{con.name}",
            con.target, true_value, sens)
        a_rows.append(row)
        b_vals.append(value)

    # ---- Step 3: exact rational projection ------------------------------
    # Noisy floats are rounded to rationals with denominator 10^6 (public,
    # deterministic). Weights are the rational inverse variances 1/scale^2.
    y = [Fraction(noisy[name]).limit_denominator(1_000_000)
         for name in buckets]
    w = [Fraction(1) / scale ** 2] * len(buckets)
    qp: QPResult = solve_qp(w, y, a_rows, b_vals,
                            max_iterations=config.max_iterations)

    projected = {name: qp.x[i] for i, name in enumerate(buckets)}
    return ReleaseResult(
        release_id=config.release_id,
        status=qp.status,
        converged=qp.converged,
        iterations=qp.iterations,
        noisy_counts=noisy,
        projected=projected,
        total_target=total_value,
        objective=qp.objective if qp.converged else None,
        kkt=qp.certificate,
        params={
            "epsilon": str(eps),
            "l1_sensitivity": str(sens),
            "noise_scale": str(scale),
            "max_iterations": config.max_iterations,
            "rounding_denominator": 1_000_000,
        },
    )


def verify_release(config: ReleaseConfig, result: ReleaseResult) -> bool:
    """Independently verify the KKT evidence of a release result."""
    if not result.converged or result.kkt is None:
        return False
    buckets = list(config.buckets)
    y = [Fraction(result.noisy_counts[name]).limit_denominator(1_000_000)
         for name in buckets]
    eps = Fraction(config.epsilon)
    sens = Fraction(config.l1_sensitivity)
    scale = sens / eps
    w = [Fraction(1) / scale ** 2] * len(buckets)
    a_rows = [[Fraction(1)] * len(buckets)]
    b_vals = [Fraction(result.total_target)]
    for con in config.constraints:
        a_rows.append([Fraction(con.coeffs.get(name, 0))
                       for name in buckets])
        # Constraint targets must be recovered from the result/ledger by
        # the verifier; here we only support public targets for
        # independent verification without the ledger.
        if not isinstance(con.target, PublicTarget):
            raise ValueError(
                "verification of noised constraint targets requires the "
                "ledger; re-run run_release with the same seed instead")
        b_vals.append(Fraction(con.target.value))
    return verify_kkt(w, y, a_rows, b_vals, result.kkt)
