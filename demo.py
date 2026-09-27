"""Run sample for poisson_process (Python 3.11, standard library only).

Every figure is labelled EXACT (analytic / deterministic) or EMPIRICAL
(Monte-Carlo estimate).  Run with:  python3.11 demo.py
"""

from poisson_process import (
    ExpStream,
    Simulator,
    homogeneous,
    piecewise_constant,
    piecewise_linear,
)


def show_events(events, limit=6):
    shown = ", ".join(f"{t:.4f}" for t in events[:limit])
    if len(events) > limit:
        shown += ", ..."
    return f"[{shown}]"


def main():
    print("=== 1. Homogeneous process (exponential inter-arrivals) ===")
    intensity = homogeneous(2.0, 0.0, 10.0)
    stream = ExpStream.from_seed(42)
    sim = Simulator(intensity, stream)
    events = sim.generate_until(10.0)
    print(f"seed={stream.seed}  draws={stream.draws}")
    print(f"events ({len(events)}): {show_events(events)}")
    print(f"EXACT     expected count = {intensity.expected_count():.4f}")

    replays = 4000
    rng_stream = ExpStream.from_seed(777)
    total = 0
    for _ in range(replays):
        total += len(Simulator(intensity, rng_stream).generate_until(10.0))
    print(f"EMPIRICAL mean count over {replays} replays (seed=777) = {total / replays:.4f}")

    print()
    print("=== 2. Piecewise-constant intensity with a zero-rate gap ===")
    gap = piecewise_constant([0.0, 1.0, 100.0, 101.0], [2.0, 0.0, 2.0])
    stream = ExpStream.from_seed(11)
    sim = Simulator(gap, stream)
    events = sim.generate_until(101.0)
    print(f"seed={stream.seed}  draws={stream.draws}")
    print(f"events ({len(events)}): {show_events(events)}  (none inside [1, 100])")
    print(f"EXACT     expected count = {gap.expected_count():.4f}")

    print()
    print("=== 3. Piecewise-linear NHPP (exact hazard inversion) ===")
    ramp = piecewise_linear([0.0, 1.0, 3.0, 4.0], [0.5, 2.0, 0.0, 1.0])
    stream = ExpStream.from_seed(2026)
    sim = Simulator(ramp, stream)
    events = sim.generate_until(4.0)
    print(f"seed={stream.seed}  draws={stream.draws}")
    print(f"events ({len(events)}): {show_events(events)}")
    print(f"EXACT     expected count = {ramp.expected_count():.4f}")

    print()
    print("=== 4. Chunked generation == whole-segment generation ===")
    whole_stream = ExpStream.from_seed(1234)
    whole = Simulator(ramp, whole_stream).generate_until(4.0)
    chunk_stream = ExpStream.from_seed(1234)
    chunked_sim = Simulator(ramp, chunk_stream)
    chunked = []
    for cut in (0.3, 1.0, 2.5):
        chunked.extend(chunked_sim.generate_until(cut))
    chunked.extend(chunked_sim.generate_until(4.0))
    print(f"EXACT     identical event lists: {whole == chunked} ({len(whole)} events)")

    print()
    print("=== 5. Checkpoint / restore ===")
    stream = ExpStream.from_seed(5)
    sim = Simulator(ramp, stream)
    head = sim.generate_until(2.5)
    checkpoint = sim.checkpoint()
    tail = sim.generate_until(4.0)
    resumed = Simulator.restore(ramp, ExpStream.from_seed(0), checkpoint)
    recovered = resumed.generate_until(4.0)
    uninterrupted = Simulator(ramp, ExpStream.from_seed(5)).generate_until(4.0)
    print(f"checkpoint at t={checkpoint['now']} (draws={checkpoint['draws']})")
    print(f"EXACT     resumed tail matches: {recovered == tail}")
    print(f"EXACT     head + resumed == uninterrupted run: {head + recovered == uninterrupted}")

    print()
    print("=== 6. Intensity version switch at a time point ===")
    stream = ExpStream.from_seed(77)
    sim = Simulator(homogeneous(1.0, 0.0, 10.0), stream)
    kept = sim.generate_until(3.0)
    sim.switch_intensity(piecewise_constant([3.0, 10.0], [4.0]))
    combined = kept + sim.generate_until(10.0)
    reference = Simulator(
        piecewise_constant([0.0, 3.0, 10.0], [1.0, 4.0]), ExpStream.from_seed(77)
    ).generate_until(10.0)
    print(f"events kept before t=3 (not resampled): {len(kept)}")
    print(f"EXACT     switched run == combined piecewise run: {combined == reference}")


if __name__ == "__main__":
    main()
