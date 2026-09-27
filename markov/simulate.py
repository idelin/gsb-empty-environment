"""Monte-Carlo estimation with a caller-injected random source.

The caller supplies the `random.Random` instance (and hence the seed); the
seed and sample size are recorded on the result so empirical estimates are
always distinguishable from exact conclusions.
"""

from dataclasses import dataclass, field
from fractions import Fraction


@dataclass
class EmpiricalResult:
    """Empirical (NOT exact) absorption estimates."""
    kind: str = "empirical"          # distinguishes from exact results
    seed: object = None              # seed recorded by the caller
    n_samples: int = 0
    start: int = 0
    absorption_counts: dict = field(default_factory=dict)  # state -> count
    absorption_frequencies: dict = field(default_factory=dict)
    mean_steps: float = None
    unfinished: int = 0              # runs that hit the step cap


def sample_path(chain, start, rng, max_steps=100000):
    """Simulate one trajectory using the caller-provided `rng`.

    Returns (absorbed_state_or_None, steps_taken).  Absorption means
    reaching a state with self-loop probability 1.
    """
    absorbing = set(chain.absorbing_states())
    state = start
    for step in range(max_steps + 1):
        if state in absorbing:
            return state, step
        row = chain.matrix[state]
        u = rng.random()
        acc = Fraction(0)
        nxt = chain.n - 1
        for j, p in enumerate(row):
            acc += p
            if u < float(acc):
                nxt = j
                break
        state = nxt
    return None, max_steps


def estimate_absorption(chain, start, rng, n_samples, seed=None,
                        max_steps=100000):
    """Empirical absorption frequencies and mean hitting time.

    `rng` is injected by the caller; `seed` is recorded for provenance.
    """
    result = EmpiricalResult(seed=seed, n_samples=n_samples, start=start)
    total_steps = 0
    finished = 0
    for _ in range(n_samples):
        state, steps = sample_path(chain, start, rng, max_steps)
        if state is None:
            result.unfinished += 1
            continue
        result.absorption_counts[state] = result.absorption_counts.get(state, 0) + 1
        total_steps += steps
        finished += 1
    result.absorption_frequencies = {
        s: c / n_samples for s, c in sorted(result.absorption_counts.items())
    }
    result.mean_steps = (total_steps / finished) if finished else None
    return result
