"""Independent checkers for the regular constraint.

These functions deliberately share no state with ``RegularConstraint``:
they re-derive answers straight from the DFA definition and the *original*
domains, so a bug in the propagator cannot leak into the verification.
"""

from __future__ import annotations

import itertools
from typing import Hashable, Iterable, List, Optional, Sequence, Set, Tuple

from automaton import DFA


def check_witness(
    dfa: DFA,
    original_domains: Sequence[Iterable[Hashable]],
    witness: Optional[Sequence[Hashable]],
) -> bool:
    """Verify a claimed solution against the original constraint.

    The witness must have one symbol per variable, every symbol must come
    from that variable's original domain, and the DFA must accept the word.
    """
    if witness is None:
        return False
    if len(witness) != len(original_domains):
        return False
    for value, domain in zip(witness, original_domains):
        if value not in domain:
            return False
    return dfa.accepts(witness)


def exhaustive_support(
    dfa: DFA, domains: Sequence[Iterable[Hashable]]
) -> Tuple[bool, List[Set[Hashable]]]:
    """Ground truth by brute-force enumeration of all sequences.

    Returns ``(satisfiable, support)`` where ``support[i]`` is the set of
    values that appear at position ``i`` in at least one accepted word.
    The empty sequence is handled naturally: ``itertools.product`` yields
    the single empty tuple, accepted iff the start state is accepting.
    """
    domain_lists = [sorted(set(d), key=repr) for d in domains]
    support: List[Set[Hashable]] = [set() for _ in domain_lists]
    satisfiable = False
    for word in itertools.product(*domain_lists):
        if dfa.accepts(word):
            satisfiable = True
            for i, value in enumerate(word):
                support[i].add(value)
    return satisfiable, support
