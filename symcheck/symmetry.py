"""Permutation canonicalization and the symmetry-condition check.

Only processes with the *same role* may be permuted: a singleton role
group (such as ``"leader"``) can never be exchanged with another process.
The canonical representative is the lexicographically smallest state over
all same-role permutations, together with the permutation that produced
it, so every counterexample keeps an explicit ID mapping and is restorable.
"""

from __future__ import annotations

from collections import deque
from itertools import permutations, product
from typing import Iterator, List, Optional, Tuple

from .model import NO_PID, Model, State


def role_groups(roles) -> List[Tuple[int, ...]]:
    """Group process indices by role, in order of first appearance."""
    groups = {}
    for pid, role in enumerate(roles):
        groups.setdefault(role, []).append(pid)
    return [tuple(members) for members in groups.values()]


def role_permutations(roles) -> Iterator[Tuple[int, ...]]:
    """Yield all permutations that permute processes within their role group."""
    groups = role_groups(roles)
    arrangements = [list(permutations(group)) for group in groups]
    n = len(roles)
    for choice in product(*arrangements):
        perm = list(range(n))
        for group, arrangement in zip(groups, choice):
            for old, new in zip(group, arrangement):
                perm[old] = new
        yield tuple(perm)


def invert(perm: Tuple[int, ...]) -> Tuple[int, ...]:
    inverse = [0] * len(perm)
    for old, new in enumerate(perm):
        inverse[new] = old
    return tuple(inverse)


def permute_state(
    state: State, perm: Tuple[int, ...], pid_vars=frozenset()
) -> State:
    """Apply ``perm`` (old pid -> new pid) to a state.

    Process IDs appearing in declared PID shared variables are renamed too;
    :data:`NO_PID` values are left untouched.
    """
    inverse = invert(perm)
    new_locals = tuple(state.locals[inverse[pid]] for pid in range(len(perm)))
    new_shared = tuple(
        perm[value] if (index in pid_vars and isinstance(value, int) and value != NO_PID) else value
        for index, value in enumerate(state.shared)
    )
    return State(shared=new_shared, locals=new_locals)


def canonicalize(state: State, roles, pid_vars=frozenset()):
    """Return ``(canonical_state, perm)``.

    ``canonical_state`` is the unique lexicographically smallest image of
    ``state`` over all same-role permutations; ``perm`` maps each original
    process ID to the ID it has in the canonical state. The original state
    is recovered with ``permute_state(canonical, invert(perm), pid_vars)``.
    """
    best_state: Optional[State] = None
    best_key = None
    best_perm: Optional[Tuple[int, ...]] = None
    for perm in role_permutations(roles):
        candidate = permute_state(state, perm, pid_vars)
        key = (candidate.shared, candidate.locals)
        if best_key is None or key < best_key:
            best_key = key
            best_state = candidate
            best_perm = perm
    return best_state, best_perm


def check_symmetry_condition(model: Model, max_states: int = 100_000):
    """Verify the transition relation is invariant under same-role permutations.

    Explores the concrete (uncompressed) reachable state space and checks:
    for every transition ``s --(pid, name)--> t`` and every same-role
    permutation ``p``, a transition ``p(s) --(p(pid), name)--> p(t)`` exists.

    Returns ``(True, None)`` if the condition holds, ``(False, witness)``
    with a concrete breaking tuple if it does not, or ``(None, None)`` when
    the exploration budget was exhausted (condition stays undecided).
    """
    identity = tuple(range(model.num_processes))
    perms = [
        perm
        for perm in role_permutations(model.roles)
        if perm != identity
    ]

    seen = {model.initial}
    queue = deque([model.initial])
    while queue:
        state = queue.popleft()
        successors = model.successors(state)
        for perm in perms:
            permuted_state = permute_state(state, perm, model.pid_vars)
            permuted_successors = {}
            for action, nxt in model.successors(permuted_state):
                permuted_successors.setdefault(action, set()).add(nxt)
            for (pid, name), target in successors:
                required_target = permute_state(target, perm, model.pid_vars)
                required_action = (perm[pid], name)
                if required_target not in permuted_successors.get(required_action, ()):
                    return False, (state, perm, (pid, name), target, required_target)
        for _, nxt in successors:
            if nxt not in seen:
                seen.add(nxt)
                if len(seen) > max_states:
                    return None, None
                queue.append(nxt)
    return True, None
