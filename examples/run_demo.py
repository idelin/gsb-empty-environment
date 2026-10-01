"""End-to-end demo: staged single-variable estimation, adaptive
multi-variable estimation (incl. correlated shared samples), and exact
finite-domain Bernoulli verification.  All stopping evidence is written
to examples/output/ as JSON.

Run from the repository root:  python3.11 examples/run_demo.py
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bounded_mean import (  # noqa: E402
    AdaptiveMultiEstimator,
    RecordingRandom,
    StagedEstimator,
    VariableSpec,
    verify_hoeffding_bernoulli,
    write_evidence,
)

OUT = Path(__file__).resolve().parent / "output"


def bernoulli(p):
    def sample(rng):
        return 1.0 if rng.random() < p else 0.0
    return sample


def part1_single():
    print("== Part 1: staged single-variable estimation ==")
    rng = RecordingRandom(seed=42)
    est = StagedEstimator(
        name="bern_0.3", low=0.0, high=1.0, epsilon=0.05, delta=0.05,
        budget=20000, sampler=bernoulli(0.3), rng=rng,
    )
    res = est.run()
    print(f"  [{res.status}] estimate={res.estimate:.4f} radius={res.radius:.4f} "
          f"after n={res.n_samples} samples (seed={res.seed}, kind={res.conclusion_kind})")
    write_evidence(res, OUT / "single_met.json")

    rng2 = RecordingRandom(seed=7)
    est2 = StagedEstimator(
        name="too_tight", low=0.0, high=1.0, epsilon=0.01, delta=0.05,
        budget=100, sampler=bernoulli(0.5), rng=rng2,
    )
    res2 = est2.run()
    print(f"  [{res2.status}] epsilon=0.01 with budget=100: radius={res2.radius:.4f} "
          f"-> target NOT certified, evidence kept ({len(res2.stages)} stages)")
    write_evidence(res2, OUT / "single_not_met.json")

    rng3 = RecordingRandom(seed=11)
    est3 = StagedEstimator(
        name="rare_event", low=0.0, high=1.0, epsilon=0.05, delta=0.05,
        budget=4096, sampler=lambda r: 0.0, rng=rng3,
        bernoulli_endpoint_rule=True,
    )
    res3 = est3.run()
    print(f"  [{res3.status}] all-zero observations: converged at n={res3.n_samples} "
          f"via rule={res3.stages[-1].rule} (Hoeffding alone needs >1600)")
    write_evidence(res3, OUT / "single_endpoint.json")


def part2_multi():
    print("== Part 2: adaptive multi-variable estimation ==")
    variables = [
        VariableSpec("const_0.7", 0.7, 0.7, 0.01, lambda r: 0.7),
        VariableSpec("bern_0.5", 0.0, 1.0, 0.05, bernoulli(0.5)),
        VariableSpec("bern_0.1", 0.0, 1.0, 0.02, bernoulli(0.1)),
    ]
    rng = RecordingRandom(seed=123)
    est = AdaptiveMultiEstimator(variables=variables, delta=0.1, budget=200000, rng=rng)
    res = est.run()
    print(f"  [{res.status}] draws_used={res.draws_used} (budget={res.budget})")
    for v in res.variables:
        print(f"    {v.name:12s} n={v.n_samples:6d} estimate={v.estimate} "
              f"radius={v.radius if v.radius is None else round(v.radius, 4)} "
              f"target={v.epsilon} [{v.status}/{v.conclusion_kind}]")
    write_evidence(res, OUT / "multi_adaptive.json")

    def joint(r):
        u = r.random()
        return {"x": u, "y": u}  # perfectly correlated, shared draws

    rng2 = RecordingRandom(seed=2024)
    est2 = AdaptiveMultiEstimator(
        variables=[VariableSpec("x", 0.0, 1.0, 0.05),
                   VariableSpec("y", 0.0, 1.0, 0.05)],
        delta=0.1, budget=100000, rng=rng2, joint_sampler=joint,
    )
    res2 = est2.run()
    print(f"  [{res2.status}] correlated shared samples: n_x="
          f"{res2.variables[0].n_samples}, n_y={res2.variables[1].n_samples} "
          "(union bound, no independence assumed)")
    write_evidence(res2, OUT / "multi_shared.json")


def part3_finite():
    print("== Part 3: exact finite-domain Bernoulli verification ==")
    report = verify_hoeffding_bernoulli(50, 0.05, grid_points=201)
    print(f"  Hoeffding radius n=50, delta=0.05: radius={report.radius:.4f}, "
          f"min coverage on 201-point grid={report.min_coverage:.4f} "
          f"at p={report.worst_p:.2f} -> ok={report.ok} (kind={report.conclusion_kind})")
    write_evidence(report, OUT / "finite_hoeffding_n50.json")


if __name__ == "__main__":
    part1_single()
    part2_multi()
    part3_finite()
    print(f"Evidence written to {OUT}")
