"""Recovery of symmetric solutions from retained representatives."""

from __future__ import annotations

from .permutations import apply_perm


def assignment_key(assignment) -> tuple:
    return tuple(sorted(assignment.items()))


def recover_symmetric(canonical_solutions, group_elements) -> list:
    """Apply every group element to every retained representative.

    Returns the deduplicated set of symmetric images.  For a sound
    symmetry-breaking run this reconstructs every solution that was
    deleted by the lex-representative constraints.
    """
    seen = {}
    for sol in canonical_solutions:
        for perm in group_elements:
            img = apply_perm(perm, sol)
            seen.setdefault(assignment_key(img), img)
    return list(seen.values())
