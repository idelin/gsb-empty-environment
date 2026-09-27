"""Sample run of the exact continuous-time Poisson process simulator.

Run with: python3.11 sample_run.py
Labels: [EXACT] = analytic / deterministic conclusion,
        [EMPIRICAL] = statistical estimate from simulation.
"""

from poisson_process import (
    PoissonSimulator,
    expected_count_homogeneous,
    expected_count_piecewise_linear,
)


def main() -> None:
    print("=== 1. Homogeneous process: exact exponential intervals ===")
    sim = PoissonSimulator(seed=20260927)
    events = sim.generate_homogeneous(rate=2.0, t_start=0.0, t_end=5.0)
    print(f"seed={sim.stream.seed}, sample size={len(events)}, draws={sim.stream.draws}")
    print(f"events={[round(e, 4) for e in events]}")
    print(f"[EXACT] analytic expected count = {expected_count_homogeneous(2.0, 0.0, 5.0)}")

    print("\n=== 2. Piecewise-linear NHPP: exact cumulative-hazard inversion ===")
    knots = [(0.0, 0.0), (3.0, 3.0), (6.0, 1.0), (9.0, 4.0)]
    sim2 = PoissonSimulator(seed=7)
    events2 = sim2.generate_piecewise_linear(knots)
    print(f"knots={knots}")
    print(f"seed={sim2.stream.seed}, sample size={len(events2)}, draws={sim2.stream.draws}")
    print(f"events={[round(e, 4) for e in events2]}")
    print(f"[EXACT] analytic expected count = {expected_count_piecewise_linear(knots)}")

    print("\n=== 3. Block generation == whole generation (same Exp(1) sequence) ===")
    whole = PoissonSimulator(seed=123).generate_piecewise_linear(
        [(0.0, 0.5), (3.0, 2.0), (6.0, 0.0), (9.0, 3.0)]
    )
    sim3 = PoissonSimulator(seed=123)
    blocked = sim3.generate_piecewise_linear(
        [(0.0, 0.5), (3.0, 2.0), (6.0, 0.0), (9.0, 3.0)], 0.0, 4.0
    )
    blocked += sim3.generate_piecewise_linear(
        [(0.0, 0.5), (3.0, 2.0), (6.0, 0.0), (9.0, 3.0)], 4.0, 9.0
    )
    max_dev = max(abs(a - b) for a, b in zip(whole, blocked))
    print(f"[EXACT] counts: whole={len(whole)}, blocked={len(blocked)}, "
          f"max |time diff|={max_dev:.2e}")

    print("\n=== 4. Intensity version switch at t=5: existing events kept ===")
    sim4 = PoissonSimulator(seed=42)
    kept = sim4.generate_homogeneous(1.0, 0.0, 5.0)
    checkpoint = sim4.checkpoint()
    tail = sim4.generate_homogeneous(4.0, 5.0, 10.0)
    print(f"kept v1 events (t<5): {[round(e, 4) for e in kept]}")
    print(f"new  v2 events (t>=5): {[round(e, 4) for e in tail]}")
    recovered = PoissonSimulator.recover(checkpoint)
    tail2 = recovered.generate_homogeneous(4.0, 5.0, 10.0)
    print(f"[EXACT] checkpoint recovery replays tail exactly: {tail == tail2}")

    print("\n=== 5. Empirical estimate vs analytic expectation ===")
    knots5 = [(0.0, 0.0), (10.0, 4.0)]
    exact = expected_count_piecewise_linear(knots5)
    n_paths = 2000
    total = sum(
        len(PoissonSimulator(seed=1000 + i).generate_piecewise_linear(knots5))
        for i in range(n_paths)
    )
    print(f"[EXACT] analytic E[N] = {exact}")
    print(f"[EMPIRICAL] mean count over {n_paths} paths = {total / n_paths:.3f}")


if __name__ == "__main__":
    main()
