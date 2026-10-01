"""Simultaneous mean estimation for several bounded variables.

Overall failure probability is controlled by a *pre-declared* error
allocation: each variable gets a share ``delta_i`` with
``sum(delta_i) <= delta``.  Within a variable, a time-uniform confidence
sequence (:func:`bounded_mean.bounds.cs_radius`) certifies the radius at
every sample size, so the policy that picks which variable to sample next
may be fully adaptive (here: the variable with the worst radius/epsilon
ratio).

Because the joint guarantee is a plain union bound over per-variable
failure events, **no independence assumption is made**: variables may be
arbitrarily correlated and may share samples (``joint_sampler`` mode).
Shared draws are never treated as independent evidence.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .bounds import cs_radius, validate_delta, validate_epsilon, validate_width
from .evidence import EMPIRICAL, EXACT, MET, NOT_MET

__all__ = ["VariableSpec", "VariableResult", "MultiResult", "AdaptiveMultiEstimator"]


@dataclass
class VariableSpec:
    name: str
    low: float
    high: float
    epsilon: float
    sampler: object = None  # callable(rng) -> float; unused in joint mode


@dataclass
class VariableResult:
    name: str
    status: str
    conclusion_kind: str
    estimate: float | None
    radius: float | None
    epsilon: float
    delta_share: float
    n_samples: int
    met_at_draw: int | None


@dataclass
class MultiResult:
    status: str  # MET iff every variable met its target
    conclusion_kind: str
    delta: float
    budget: int
    draws_used: int
    seed: object
    rng_draws: int | None
    shared_samples: bool
    variables: list = field(default_factory=list)
    selection_history: list = field(default_factory=list)
    guarantee: str = ""

    def to_dict(self) -> dict:
        from dataclasses import asdict

        return asdict(self)


class AdaptiveMultiEstimator:
    GUARANTEE = (
        "P(for all i: |estimate_i - mean_i| <= epsilon_i) >= 1 - delta: "
        "union bound over pre-declared per-variable shares, each certified "
        "by a time-uniform confidence sequence. Valid for arbitrary "
        "dependence between variables; shared samples are not treated as "
        "independent."
    )

    def __init__(
        self,
        *,
        variables: list[VariableSpec],
        delta: float,
        budget: int,
        rng,
        allocation: dict[str, float] | None = None,
        joint_sampler=None,
    ):
        self.delta = validate_delta(delta)
        if isinstance(budget, bool) or not isinstance(budget, int) or budget < 1:
            raise ValueError(f"budget must be a positive integer, got {budget!r}")
        self.budget = budget
        self._rng = rng
        self._joint = joint_sampler
        if not variables:
            raise ValueError("at least one variable is required")
        names = [v.name for v in variables]
        if len(set(names)) != len(names):
            raise ValueError("variable names must be unique")
        self._vars = list(variables)
        for v in self._vars:
            validate_width(float(v.low), float(v.high))
            v.epsilon = validate_epsilon(v.epsilon)
            if joint_sampler is None and not callable(v.sampler):
                raise TypeError(
                    f"variable {v.name!r} needs a sampler (or pass joint_sampler)"
                )
        self._shares = self._resolve_allocation(allocation, names)

    def _resolve_allocation(self, allocation, names) -> dict[str, float]:
        if allocation is None:
            share = self.delta / len(names)
            return {name: share for name in names}
        shares = {}
        for name in names:
            if name not in allocation:
                raise ValueError(f"allocation missing variable {name!r}")
            shares[name] = validate_delta(allocation[name])
        total = sum(shares.values())
        if total > self.delta * (1.0 + 1e-12):
            raise ValueError(
                f"allocation sums to {total}, exceeding delta={self.delta}: "
                "overall failure probability would not be controlled"
            )
        return shares

    def run(self) -> MultiResult:
        seed = getattr(self._rng, "seed", None)
        state = {
            v.name: {
                "n": 0, "total": 0.0, "met": False, "met_at": None,
                "exact": False, "estimate": None, "radius": float("inf"),
            }
            for v in self._vars
        }
        by_name = {v.name: v for v in self._vars}

        # Constant variables converge immediately and exactly.
        for v in self._vars:
            if float(v.low) == float(v.high):
                st = state[v.name]
                st.update(met=True, met_at=0, exact=True,
                          estimate=float(v.low), radius=0.0)

        def radius_of(v, n):
            if n == 0:
                return float("inf")
            return cs_radius(n, float(v.high) - float(v.low), self._shares[v.name])

        history: list[dict] = []
        draws = 0
        while draws < self.budget:
            unmet = [v for v in self._vars if not state[v.name]["met"]]
            if not unmet:
                break
            draws += 1
            if self._joint is not None:
                values = self._joint(self._rng)
                if not isinstance(values, dict):
                    raise TypeError("joint_sampler must return {name: value}")
                chosen = sorted(values)
                for name, x in values.items():
                    if name not in state:
                        raise ValueError(f"joint_sampler returned unknown variable {name!r}")
                    st = state[name]
                    if st["met"]:
                        continue
                    spec = by_name[name]
                    x = float(x)
                    if not float(spec.low) <= x <= float(spec.high):
                        raise ValueError(
                            f"joint sample for {name!r} out of bounds: {x!r}"
                        )
                    st["n"] += 1
                    st["total"] += x
            else:
                # Adaptive choice: worst certified radius relative to target.
                v = max(unmet, key=lambda u: radius_of(u, state[u.name]["n"]) / u.epsilon)
                x = float(v.sampler(self._rng))
                if not float(v.low) <= x <= float(v.high):
                    raise ValueError(f"sample for {v.name!r} out of bounds: {x!r}")
                st = state[v.name]
                st["n"] += 1
                st["total"] += x
                chosen = [v.name]
            for v in unmet:
                st = state[v.name]
                if st["n"] > 0:
                    st["estimate"] = st["total"] / st["n"]
                    st["radius"] = radius_of(v, st["n"])
                    if st["radius"] <= v.epsilon:
                        st["met"] = True
                        st["met_at"] = draws
            history.append({"draw": draws, "sampled": chosen})

        results = []
        for v in self._vars:
            st = state[v.name]
            results.append(
                VariableResult(
                    name=v.name,
                    status=MET if st["met"] else NOT_MET,
                    conclusion_kind=EXACT if st["exact"] else EMPIRICAL,
                    estimate=st["estimate"],
                    radius=None if st["radius"] == float("inf") else st["radius"],
                    epsilon=v.epsilon,
                    delta_share=self._shares[v.name],
                    n_samples=st["n"],
                    met_at_draw=st["met_at"],
                )
            )
        overall = MET if all(r.status == MET for r in results) else NOT_MET
        guarantee = self.GUARANTEE
        if overall == NOT_MET:
            guarantee += (
                " BUDGET EXHAUSTED: at least one variable did NOT reach its "
                "error target; the joint guarantee does not hold for this run."
            )
        return MultiResult(
            status=overall,
            conclusion_kind=EMPIRICAL,
            delta=self.delta,
            budget=self.budget,
            draws_used=draws,
            seed=seed,
            rng_draws=getattr(self._rng, "draws", None),
            shared_samples=self._joint is not None,
            variables=results,
            selection_history=history,
            guarantee=guarantee,
        )
