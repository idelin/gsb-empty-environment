"""Symmetry reduction via exact net automorphisms.

A place permutation is admissible only if it maps the net onto itself:
capacities are preserved and the multiset of transition arc patterns
(inputs / outputs / reads / inhibitors) is preserved up to relabelling of
transitions. Such a permutation maps enabled transitions to enabled
transitions, so canonicalising markings under the group preserves enabling
conditions exactly -- the reduced search explores one concrete
representative per symmetry class and stays sound and complete for
reachability (and for symmetric predicates).

The canonical form of a marking is the lexicographic minimum over its
orbit. If the automorphism search space exceeds ``MAX_PERMUTATIONS`` the
reduction safely falls back to the identity (no reduction).
"""
from __future__ import annotations

from collections import Counter
from itertools import permutations, product

from .model import PetriNet


def _transition_key(t) -> tuple:
    return (t.inputs, t.outputs, t.reads, t.inhibitors)


def _permute_key(key: tuple, image: dict[str, str]) -> tuple:
    return tuple(
        tuple(sorted((image[p], w) for p, w in arcs)) for arcs in key
    )


class SymmetryReduction:
    MAX_PERMUTATIONS = 4096

    def __init__(self, net: PetriNet) -> None:
        self.net = net
        self.permutations: list[tuple[int, ...]] = self._compute_automorphisms()

    # ------------------------------------------------------------------ #
    def _compute_automorphisms(self) -> list[tuple[int, ...]]:
        """All place permutations (as image tuples) preserving the net."""
        n = len(self.net.places)
        identity = tuple(range(n))

        # Candidate permutations must preserve capacity and the per-place
        # totals of each arc kind: group place indices by that signature
        # and permute only within each class (a safe, cheap pre-filter).
        totals = {p: [0, 0, 0, 0] for p in self.net.places}
        for t in self.net.transitions.values():
            for kind, arcs in enumerate((t.inputs, t.outputs, t.reads, t.inhibitors)):
                for place, weight in arcs:
                    totals[place][kind] += weight
        classes: dict[tuple, list[int]] = {}
        for i, p in enumerate(self.net.places):
            signature = (self.net.capacities[p], *totals[p])
            classes.setdefault(signature, []).append(i)

        space = 1
        for members in classes.values():
            for k in range(2, len(members) + 1):
                space *= k
            if space > self.MAX_PERMUTATIONS:
                return [identity]  # too large: safe fallback, no reduction

        original = Counter(
            _transition_key(t) for t in self.net.transitions.values()
        )

        per_class = [
            [dict(zip(members, perm)) for perm in permutations(members)]
            for members in classes.values()
        ]
        result = []
        for combo in product(*per_class):
            mapping: dict[int, int] = {}
            for part in combo:
                mapping.update(part)
            image_names = {
                self.net.places[i]: self.net.places[mapping[i]] for i in range(n)
            }
            permuted = Counter(
                _permute_key(_transition_key(t), image_names)
                for t in self.net.transitions.values()
            )
            if permuted == original:
                result.append(tuple(mapping[i] for i in range(n)))
        return result or [identity]

    # ------------------------------------------------------------------ #
    def canonical_with_perm(
        self, marking: tuple[int, ...]
    ) -> tuple[tuple[int, ...], tuple[int, ...]]:
        """Canonical form plus a witnessing permutation (image indices)."""
        best = marking
        best_perm = self.permutations[0]
        for perm in self.permutations:
            image = [0] * len(marking)
            for i, j in enumerate(perm):
                image[j] = marking[i]
            image_t = tuple(image)
            if image_t < best:
                best = image_t
                best_perm = perm
        return best, best_perm

    def canonical(self, marking: tuple[int, ...]) -> tuple[int, ...]:
        """Lexicographic minimum of the marking's orbit under the group."""
        return self.canonical_with_perm(marking)[0]

    # ------------------------------------------------------------------ #
    def _enabled_key_multiset(self, marking: tuple[int, ...]) -> Counter:
        return Counter(
            _transition_key(t) for t in self.net.enabled_transitions(marking)
        )

    def preserves_enabling(self, marking: tuple[int, ...]) -> bool:
        """Check enabling conditions survive canonicalisation.

        Let pi be the automorphism mapping ``marking`` to its canonical
        form. Enabling is preserved iff the enabled arc patterns at the
        canonical form are exactly the pi-images of those at ``marking``.
        """
        canon, perm = self.canonical_with_perm(marking)
        image = {
            self.net.places[i]: self.net.places[perm[i]]
            for i in range(len(self.net.places))
        }
        expected = Counter(
            _permute_key(_transition_key(t), image)
            for t in self.net.enabled_transitions(marking)
        )
        return expected == self._enabled_key_multiset(canon)
