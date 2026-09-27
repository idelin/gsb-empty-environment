"""Strong lumpability: exact verification of a proposed state grouping.

A partition is strongly lumpable iff for every pair of groups (A, B) the
total probability of moving from any state in A into B is the same for all
states of A.  Only then may the chain be compressed onto the groups without
changing the observable (group-level) dynamics.  When the check fails we
return an exact witness instead of compressing blindly.
"""

from fractions import Fraction
from dataclasses import dataclass

from .chain import Chain


@dataclass
class LumpabilityWitness:
    """Exact certificate that a partition is NOT strongly lumpable."""
    group: int            # index of the group containing the two states
    state_i: int
    state_j: int
    target_group: int     # index of the group they flow into at different rates
    rate_i: Fraction      # sum_k in target_group P[state_i][k]
    rate_j: Fraction      # sum_k in target_group P[state_j][k]

    def __str__(self):
        return (f"states {self.state_i} and {self.state_j} (both in group "
                f"{self.group}) move to group {self.target_group} with "
                f"different exact rates {self.rate_i} != {self.rate_j}")


def check_strong_lumpability(chain, groups):
    """Verify `groups` (list of iterables of states) is strongly lumpable.

    Returns (True, None) if the partition is valid and strongly lumpable,
    otherwise (False, LumpabilityWitness) with an exact rational witness.
    Blind compression is refused: callers must branch on the boolean.
    """
    groups = [frozenset(g) for g in groups]
    union = set().union(*groups) if groups else set()
    if union != set(range(chain.n)) or sum(len(g) for g in groups) != chain.n:
        raise ValueError("groups must be a partition of all states")
    group_of = {}
    for gi, g in enumerate(groups):
        for s in g:
            group_of[s] = gi
    for gi, g in enumerate(groups):
        for gj, h in enumerate(groups):
            rates = {}
            for s in g:
                rates[s] = sum((chain.matrix[s][k] for k in h), Fraction(0))
            it = iter(sorted(g))
            first = next(it)
            for other in it:
                if rates[other] != rates[first]:
                    return False, LumpabilityWitness(
                        group=gi, state_i=first, state_j=other,
                        target_group=gj,
                        rate_i=rates[first], rate_j=rates[other])
    return True, None


def lumped_chain(chain, groups):
    """Compress the chain onto `groups`; refuses if not strongly lumpable.

    Returns a new Chain whose states are group indices.  Raises ValueError
    (carrying the witness) when the grouping fails verification.
    """
    groups = [frozenset(g) for g in groups]
    ok, witness = check_strong_lumpability(chain, groups)
    if not ok:
        raise ValueError(f"partition is not strongly lumpable: {witness}")
    matrix = []
    for g in groups:
        rep = min(g)
        row = []
        for h in groups:
            row.append(sum((chain.matrix[rep][k] for k in h), Fraction(0)))
        matrix.append(row)
    return Chain(matrix)
