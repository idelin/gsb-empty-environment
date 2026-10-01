"""Runnable demo: stable evaluation, DAG composition, certified inversion.

All parameters below are synthetic.  The numbers demonstrate numerical
correctness of the formula  eps' = log(1 + q*(exp(eps) - 1))  -- they are
not, by themselves, a privacy proof.
"""

from amplification import dag, invert, reference, stable
from amplification.assumptions import AssumptionError, SamplingAssumption


def main() -> None:
    print("=" * 72)
    print("1. Stable point evaluation  eps' = log(1 + q*(exp(eps) - 1))")
    print("=" * 72)
    cases = [
        ("boundary q=0", 0.0, 3.0),
        ("boundary q=1", 1.0, 3.0),
        ("tiny q", 1e-12, 1.0),
        ("tiny eps", 0.5, 1e-8),
        ("moderate", 0.1, 1.0),
        ("huge eps (exp would overflow)", 1e-3, 1000.0),
        ("huge eps, tiny q", 1e-300, 800.0),
    ]
    for label, q, eps in cases:
        got = stable.eps_prime(q, eps)
        ref = reference.reference_eps_prime(q, eps)
        print(f"  {label:32s} q={q:<8.2g} eps={eps:<7.2g} "
              f"eps'={got:.16g}  (series ref: {float(ref):.16g})")

    print()
    print("=" * 72)
    print("2. Parameter DAG: 10-round composition with interval evaluation")
    print("=" * 72)
    assumption = SamplingAssumption(scheme="poisson", adjacency="add_remove",
                                    dataset_size=50_000)
    rounds = [dag.EpsPrime(dag.Const(0.02), dag.Const(1.5), assumption)
              for _ in range(10)]
    comp = dag.compose_rounds(rounds)
    print(f"  per-round eps'      = {stable.eps_prime(0.02, 1.5):.16g}")
    print(f"  composed (point)    = {comp.eval_point({}):.16g}")
    print(f"  composed (interval) = {comp.eval_interval({})}")

    print()
    print("=" * 72)
    print("3. Budget inversion: max q with certified enclosing interval")
    print("=" * 72)
    for eps, budget in ((3.0, 0.2), (1000.0, 690.0), (10.0, 0.01)):
        r = invert.max_q_for_budget(eps, budget)
        ref_q = float(reference.reference_max_q(eps, budget))
        print(f"  eps={eps}, budget={budget}")
        print(f"    q* analytic        = {r.q_analytic:.16g}")
        print(f"    certified bracket  = [{r.q_lo:.16g}, {r.q_hi:.16g}]"
              f"  (width {r.bracket_width:.3g})")
        print(f"    series reference   = {ref_q:.16g}  (inside bracket: "
              f"{r.q_lo <= ref_q <= r.q_hi})")
        print(f"    eps'(q_lo) interval = {r.eps_at_q_lo}  residual {r.residual_lo:.3g}")
        print(f"    eps'(q_hi) interval = {r.eps_at_q_hi}  residual {r.residual_hi:.3g}")

    print()
    print("=" * 72)
    print("4. Model-mismatch rejection (formula not stretched beyond its model)")
    print("=" * 72)
    for bad in ({"scheme": "without_replacement"}, {"base_delta": 1e-5}):
        try:
            dag.EpsPrime(dag.Const(0.1), dag.Const(1.0), SamplingAssumption(**bad))
        except AssumptionError as exc:
            print(f"  rejected {bad}: {exc}")
    for q, eps in ((1.5, 1.0), (0.5, -1.0)):
        try:
            stable.eps_prime(q, eps)
        except ValueError as exc:
            print(f"  rejected q={q}, eps={eps}: {exc}")


if __name__ == "__main__":
    main()
