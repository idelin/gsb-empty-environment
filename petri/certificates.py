"""Linear invariant certificates with incremental revalidation.

A certificate is a weight vector ``y`` over places such that ``y . C = 0``
for the flow matrix ``C`` of the net (columns = transitions). Then
``y . M = y . M0`` for every reachable marking ``M`` -- a sound invariant
that can be checked per marking in O(|places|) without any search.

Only token-moving arcs (inputs/outputs) appear in the flow matrix; read and
inhibitor arcs never move tokens, so modifying them cannot invalidate a
certificate.

When transitions are modified incrementally via ``PetriNet.modify_transition``,
``CertificateCache.revalidate()`` rechecks only the changed flow columns
instead of the whole net.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .engine import SearchResult
from .model import PetriNet

VALID = "VALID"
INVALID = "INVALID"


@dataclass
class Certificate:
    name: str
    weights: dict[str, int]
    constant: int  # y . M0
    revision: int  # net revision this certificate is validated against
    status: str  # VALID | INVALID
    violations: set[str] = field(default_factory=set)  # offending transitions
    checked_transitions: frozenset[str] = frozenset()  # columns of last check

    def holds_on(self, marking: dict[str, int]) -> bool:
        return sum(self.weights.get(p, 0) * marking.get(p, 0) for p in self.weights) == self.constant


class CertificateCache:
    """Caches certificates and revalidates them against net edits."""

    def __init__(self, net: PetriNet) -> None:
        self.net = net
        self._certs: dict[str, Certificate] = {}

    # ------------------------------------------------------------------ #
    def _violating(self, weights: dict[str, int], names) -> set[str]:
        """Transitions whose flow column is not annihilated by ``weights``."""
        bad = set()
        for name in names:
            t = self.net.transitions[name]
            flow = sum(
                weights.get(p, 0) * d for p, d in t.flow_delta().items()
            )
            if flow != 0:
                bad.add(name)
        return bad

    # ------------------------------------------------------------------ #
    def register(self, name: str, weights: dict[str, int]) -> Certificate:
        """Register a certificate after a full soundness check (y . C = 0)."""
        for place in weights:
            if place not in self.net.pindex:
                raise ValueError(f"unknown place {place!r} in certificate weights")
        bad = self._violating(weights, self.net.transitions)
        if bad:
            raise ValueError(
                f"not a net invariant: token flow not conserved for {sorted(bad)}"
            )
        initial = self.net.marking_dict(self.net.initial)
        constant = sum(weights.get(p, 0) * initial.get(p, 0) for p in weights)
        cert = Certificate(
            name=name,
            weights=dict(weights),
            constant=constant,
            revision=self.net.revision,
            status=VALID,
            violations=set(),
            checked_transitions=frozenset(self.net.transitions),
        )
        self._certs[name] = cert
        return cert

    def get(self, name: str) -> Certificate:
        return self._certs[name]

    # ------------------------------------------------------------------ #
    def revalidate(self) -> dict[str, str]:
        """Incrementally revalidate cached certificates after net edits.

        Only transitions changed since each certificate's revision are
        rechecked; unchanged columns of y . C remain zero by linearity.
        Returns a name -> status mapping.
        """
        report = {}
        for cert in self._certs.values():
            if cert.revision == self.net.revision:
                report[cert.name] = cert.status
                continue
            changed = {
                tname
                for rev, tname in self.net.changelog
                if rev > cert.revision
            }
            bad = self._violating(cert.weights, changed)
            cert.violations = (cert.violations - changed) | bad
            cert.status = VALID if not cert.violations else INVALID
            cert.revision = self.net.revision
            cert.checked_transitions = frozenset(changed)
            report[cert.name] = cert.status
        return report

    # ------------------------------------------------------------------ #
    def check_reachable(self, name: str, result: SearchResult) -> bool:
        """Confirm a VALID certificate on every explored concrete marking."""
        cert = self._certs[name]
        if cert.status != VALID:
            return False
        return all(
            cert.holds_on(self.net.marking_dict(rep))
            for rep in result.representatives.values()
        )

    def verify_terminal_token_totals(
        self, name: str, result: SearchResult
    ) -> dict[tuple[int, ...], bool]:
        """Verify the invariant on every deadlock (terminal) state.

        With all-ones weights this is precisely the check that the total
        token count of each terminal marking matches the net invariant.
        """
        cert = self._certs[name]
        return {
            key: cert.status == VALID
            and cert.holds_on(self.net.marking_dict(result.representatives[key]))
            for key in result.deadlock_keys()
        }
