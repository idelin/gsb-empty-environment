"""Place-invariant certificates.

A certificate is a weight vector w over places.  It is *verified* (not
computed) against the net: for every transition t the weighted token effect
sum_p w[p] * (produced(t,p) - consumed(t,p)) must be 0.  A valid certificate
implies sum_p w[p] * m[p] is constant over all reachable markings, so the
expected value is fixed by the initial marking.  Read and inhibitor arcs
move no tokens and do not appear in the effect.
"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class InvariantCertificate:
    name: str
    weights: dict = field(default_factory=dict)  # place_id -> int weight

    def __post_init__(self):
        for pid, w in self.weights.items():
            if not isinstance(w, int) or isinstance(w, bool):
                raise ValueError(f"certificate {self.name!r}: weight of "
                                 f"{pid!r} must be an integer")


def transition_effect(net, t):
    """Net token delta per place: outputs - inputs (self-loops cancel out)."""
    eff = {}
    for pid, w in t.inputs.items():
        eff[pid] = eff.get(pid, 0) - w
    for pid, w in t.outputs.items():
        eff[pid] = eff.get(pid, 0) + w
    return eff


def verify_certificate(net, cert, only_transitions=None):
    """Check cert against transitions.  `only_transitions` restricts the
    check to a subset (used for incremental revalidation); unknown ids
    (e.g. removed transitions) are skipped."""
    tids = only_transitions if only_transitions is not None else net.transitions
    for tid in tids:
        t = net.transitions.get(tid)
        if t is None:
            continue
        delta = sum(cert.weights.get(pid, 0) * d
                    for pid, d in transition_effect(net, t).items())
        if delta != 0:
            return False
    return True


def invariant_value(net, cert, marking):
    """Weighted token sum of a marking under the certificate."""
    return sum(w * marking[net.index[pid]]
               for pid, w in cert.weights.items())
