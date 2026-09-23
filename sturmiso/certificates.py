"""Root-count certificates: the auditable evidence behind every interval."""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Interval
from .sturm import EndpointSign


@dataclass(frozen=True)
class RootCountCertificate:
    """Certificate that a square-free polynomial has exactly ``count``
    distinct real roots in ``interval``.

    The evidence is the Sturm-chain sign vector at each endpoint
    (``lower_endpoint`` / ``lower_endpoint``) together with the counting
    rule; ``count == lower_endpoint.variation - upper_endpoint.variation``.
    An independent verifier can recompute everything from the polynomial,
    the interval and this certificate (see verify.py).
    """

    interval: Interval
    chain_degrees: tuple  # degrees of the Sturm chain members
    lower_endpoint: EndpointSign
    upper_endpoint: EndpointSign
    count: int
    rule: str

    def check_arithmetic(self) -> bool:
        return (
            self.count
            == self.lower_endpoint.variation - self.upper_endpoint.variation
        )
