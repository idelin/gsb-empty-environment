"""Independent oracle for cross-checking the incremental network.

Everything here is implemented independently from scratch:

* constraints are re-expanded into edges by this module;
* feasibility is decided by Bellman-Ford from a virtual zero source;
* earliest/latest windows are computed by an independent Floyd-Warshall over
  a freshly built matrix;
* every schedule handed in is checked directly against the raw inequalities.

The two code paths share no bookkeeping, so agreement gives real coverage of
the incremental propagation and of retraction ("bounds must relax when
support is withdrawn").
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

__all__ = ["Verifier", "RandomScenario"]

_Z = "Z"


@dataclass(frozen=True)
class _VConstraint:
    cid: str
    a: Optional[str]
    b: str
    lo: Optional[int]
    hi: Optional[int]


class Verifier:
    """Independent batch (non-incremental) difference-constraints oracle."""

    def __init__(self) -> None:
        self.events: List[str] = []
        self.constraints: List[_VConstraint] = []
        self._has_anchor = False

    def add_event(self, name: str) -> None:
        if name not in self.events:
            self.events.append(name)

    def add_interval(self, cid: str, a: Optional[str], b: str,
                     lo: Optional[int], hi: Optional[int]) -> None:
        if a is not None:
            self.add_event(a)
        else:
            self._has_anchor = True
        self.add_event(b)
        self.constraints.append(_VConstraint(cid, a, b, lo, hi))

    # ------------------------------------------------------------------ #
    def _edges(self) -> List[Tuple[str, str, int, str]]:
        edges: List[Tuple[str, str, int, str]] = []
        for c in self.constraints:
            src_a = c.a if c.a is not None else _Z
            if c.hi is not None:
                edges.append((src_a, c.b, c.hi, c.cid))
            if c.lo is not None:
                edges.append((c.b, src_a, -c.lo, c.cid))
        return edges

    def feasible(self) -> bool:
        """Bellman-Ford feasibility from a virtual zero-distance source."""
        nodes = list(self.events)
        if self._has_anchor and _Z not in nodes:
            nodes = [_Z] + nodes
        dist = {v: 0 for v in nodes}
        edges = self._edges()
        for _ in range(len(nodes)):
            changed = False
            for u, v, w, _ in edges:
                if dist[u] + w < dist[v]:
                    dist[v] = dist[u] + w
                    changed = True
            if not changed:
                return True
        return False

    def distances(self) -> Tuple[List[str], Dict[Tuple[str, str], Optional[int]]]:
        """Fresh Floyd-Warshall shortest paths. ``None`` entries mean +inf."""
        nodes = list(self.events)
        if self._has_anchor and _Z not in nodes:
            nodes = [_Z] + nodes
        d: Dict[Tuple[str, str], Optional[int]] = {
            (u, v): (0 if u == v else None) for u in nodes for v in nodes
        }
        for u, v, w, _ in self._edges():
            cur = d[(u, v)]
            if cur is None or w < cur:
                d[(u, v)] = w
        for k in nodes:
            for i in nodes:
                dik = d[(i, k)]
                if dik is None:
                    continue
                for j in nodes:
                    dkj = d[(k, j)]
                    if dkj is None:
                        continue
                    cand = dik + dkj
                    cur = d[(i, j)]
                    if cur is None or cand < cur:
                        d[(i, j)] = cand
        return nodes, d

    def bounds(self) -> Dict[str, Tuple[Optional[int], Optional[int]]]:
        """Tight windows relative to Z=0 when anchored; else unbounded."""
        nodes, d = self.distances()
        out: Dict[str, Tuple[Optional[int], Optional[int]]] = {}
        for ev in self.events:
            if self._has_anchor:
                latest = d[(_Z, ev)]
                earliest = None if d[(ev, _Z)] is None else -d[(ev, _Z)]
                out[ev] = (earliest, latest)
            else:
                out[ev] = (None, None)
        return out

    def check_assignment(self, values: Dict[str, int]) -> List[str]:
        vals = dict(values)
        vals[_Z] = 0
        bad: List[str] = []
        for c in self.constraints:
            a = c.a if c.a is not None else _Z
            delta = vals[c.b] - vals[a]
            if c.lo is not None and delta < c.lo:
                bad.append(c.cid)
            if c.hi is not None and delta > c.hi:
                bad.append(c.cid)
        return bad

    # ------------------------------------------------------------------ #
    def check_network(self, network) -> List[str]:
        """Compare an oracle view against a live ``MinMaxNetwork``.

        Returns a list of disagreement descriptions (empty == agreement).
        """
        problems: List[str] = []
        if self.feasible() != (not network.infeasible):
            problems.append(
                f"feasibility mismatch: oracle={self.feasible()} "
                f"network={not network.infeasible}"
            )
            return problems  # bounds are meaningless when disagreeing here

        oracle_bounds = self.bounds()
        live_bounds = network.all_bounds()
        for ev in self.events:
            ob = oracle_bounds.get(ev, (None, None))
            lb = live_bounds.get(ev)
            lb_pair = (lb.earliest, lb.latest) if lb is not None else (None, None)
            if ob != lb_pair:
                problems.append(f"bounds mismatch for {ev}: oracle={ob} network={lb_pair}")

        if not network.infeasible:
            schedule = network.solve()
            violated = self.check_assignment(schedule)
            if violated:
                problems.append(f"schedule violates constraints: {violated}")
        return problems


@dataclass
class RandomScenario:
    """Configurable random stream of adds/pops for differential testing."""

    seed: int = 1234
    n_events: int = 6
    n_steps: int = 300
    max_weight: int = 20
    pop_probability: float = 0.25
    anchor_probability: float = 0.3
    rng: random.Random = None  # type: ignore[assignment]

    def __post_init__(self) -> None:
        if self.rng is None:
            self.rng = random.Random(self.seed)


class _RetractableVerifier(Verifier):
    """Verifier with its own independent push/pop bookkeeping."""

    def __init__(self) -> None:
        super().__init__()
        self._levels: List[List[str]] = []

    def push(self) -> None:
        self._levels.append([])

    def pop(self) -> None:
        cids = set(self._levels.pop())
        if cids:
            self.constraints = [c for c in self.constraints if c.cid not in cids]
            self._has_anchor = any(
                c.a is None for c in self.constraints
            )

    def add_interval(self, cid, a, b, lo, hi) -> None:
        super().add_interval(cid, a, b, lo, hi)
        if self._levels:
            self._levels[-1].append(cid)


def run_differential(scenario: Optional[RandomScenario] = None,
                     verbose: bool = False) -> int:
    """Drive random add/pop updates through both implementations.

    Returns the number of checkpoints compared.  Raises ``AssertionError``
    on the first oracle disagreement.  A full batch oracle (this module) is
    rebuilt implicitly on every comparison -- independent of the network's
    incremental machinery.
    """
    from .network import MinMaxNetwork, InconsistentNetwork

    sc = scenario or RandomScenario()
    rng = sc.rng
    checks = 0

    def fresh():
        events = [f"e{i}" for i in range(sc.n_events)]
        net = MinMaxNetwork(events)
        ora = _RetractableVerifier()
        for ev in events:
            ora.add_event(ev)
        return net, ora

    net, ora = fresh()
    counter = 0
    for step in range(sc.n_steps):
        # Retract together.
        if net.depth() > 0 and rng.random() < sc.pop_probability:
            net.pop()
            ora.pop()
            problems = ora.check_network(net)
            assert not problems, problems
            checks += 1
            continue

        net.push()
        ora.push()
        k = rng.randint(1, 3)
        for _ in range(k):
            counter += 1
            cid = f"c{counter}"
            absolute = rng.random() < sc.anchor_probability
            a = None if absolute else rng.choice(ora.events)
            b = rng.choice(ora.events)
            if a is not None and a == b and rng.random() < 0.5:
                # self difference constraint forces 0 in [lo,hi]
                lo = rng.choice([None, rng.randint(-2, 0)])
                hi = rng.choice([None, rng.randint(0, 2)])
            else:
                lo = rng.randint(-sc.max_weight, sc.max_weight)
                hi = rng.randint(-sc.max_weight, sc.max_weight)
                if lo > hi:
                    lo, hi = hi, lo
                lo = rng.choice([None, lo])
                hi = rng.choice([None, hi])
            try:
                net.add_interval(cid, a, b, lo, hi)
                net_raised = False
            except InconsistentNetwork:
                net_raised = True
            ora.add_interval(cid, a, b, lo, hi)
            ora_feasible = ora.feasible()
            if net_raised:
                assert not ora_feasible, (cid, a, b, lo, hi)
            else:
                assert ora_feasible, (cid, a, b, lo, hi)

        if net.infeasible:
            # Contradictory hypotheses: restart both sides with same seed
            # state (events only) and continue the random stream.
            checks += 1
            net, ora = fresh()
            continue

        problems = ora.check_network(net)
        assert not problems, problems
        checks += 1

        # Occasionally keep the hypothesis, occasionally retract it.
        if rng.random() < 0.5 and net.depth() > 0:
            net.commit()
            # fold oracle top level into parent by rebuilding its level tags
            top = ora._levels.pop()
            if ora._levels:
                ora._levels[-1].extend(top)

        if verbose and step % 50 == 0:
            print(f"step {step}: {checks} agreements, {counter} constraints")
    return checks
