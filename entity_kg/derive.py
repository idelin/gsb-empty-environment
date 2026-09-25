"""Pure derivation: active evidence -> entities, steps, conflict cycles.

Determinism guarantees:
  * graph traversal visits nodes / neighbors in sorted order;
  * derived chains use canonical (ordered) endpoints;
  * step and conflict ids are content hashes, so replaying the same log
    regenerates the exact same ids regardless of machine or run.
"""

from __future__ import annotations

from collections import deque

from .model import (
    Conflict,
    CycleEdge,
    DerivedState,
    Entity,
    Fact,
    FactKind,
    PremiseEdge,
    Step,
    digest,
    make_step_id,
)
from .rules import rule

STEP_VERSION = 1
CONFLICT_VERSION = 1


def _bfs_tree(root: str,
              adj: dict[str, list[str]]) -> dict[str, str | None]:
    parent: dict[str, str | None] = {root: None}
    queue: deque[str] = deque([root])
    while queue:
        node = queue.popleft()
        for nxt in adj[node]:
            if nxt not in parent:
                parent[nxt] = node
                queue.append(nxt)
    return parent


def _root_path(parent: dict[str, str | None], node: str) -> list[str]:
    path: list[str] = []
    while node is not None:
        path.append(node)
        node = parent[node]  # type: ignore[assignment]
    path.reverse()
    return path


def _path_between(px: list[str], py: list[str]) -> list[str]:
    """Path x->y inside the shared BFS tree, given x->root and y->root paths."""
    depth = 0
    while depth < min(len(px), len(py)) and px[depth] == py[depth]:
        depth += 1
    lca = px[depth - 1]
    return list(reversed(px[depth:])) + [lca] + py[depth:]


def derive(facts: dict[str, Fact],
           labels: dict[str, str],
           known_nodes: set[str],
           state_version: int) -> DerivedState:
    active = [f for f in facts.values() if f.active]
    eq_facts = [f for f in active if f.kind is FactKind.EQ]
    neq_facts = [f for f in active if f.kind is FactKind.NEQ]

    nodes: set[str] = set(known_nodes)
    for fact in active:
        nodes.add(fact.a)
        nodes.add(fact.b)

    adj: dict[str, list[str]] = {n: [] for n in nodes}
    edge_fact: dict[tuple[str, str], str] = {}
    for fact in eq_facts:
        x, y = fact.ordered
        adj[x].append(y)
        adj[y].append(x)
        edge_fact[(x, y)] = fact.fid
    for node in adj:
        adj[node].sort()

    components: list[list[str]] = []
    seen: set[str] = set()
    representatives: dict[str, str] = {}
    for source in sorted(nodes):
        if source in seen:
            continue
        component = sorted(_bfs_tree(source, adj))
        components.append(component)
        seen.update(component)
        rep = component[0]
        for member in component:
            representatives[member] = rep

    steps: dict[str, Step] = {}
    pair_step: dict[tuple[str, str], str] = {}
    for component in components:
        rep = component[0]
        tree = _bfs_tree(rep, adj)
        root_paths = {m: _root_path(tree, m) for m in component}
        for i, x in enumerate(component):
            for y in component[i + 1:]:
                pair = (x, y)  # component is sorted, so pair is canonical
                if pair in edge_fact:
                    continue  # directly asserted edge needs no derived step
                path = _path_between(root_paths[x], root_paths[y])
                chain_facts = [
                    edge_fact[tuple(sorted((path[k], path[k + 1])))]
                    for k in range(len(path) - 1)
                ]
                sid = make_step_id("R-EQ-TRANSITIVE", chain_facts, x, y)
                if sid not in steps:
                    chain = [
                        PremiseEdge(
                            step_id=None,
                            fact_id=edge_fact[tuple(sorted(
                                (path[k], path[k + 1])))],
                            a=path[k],
                            b=path[k + 1],
                        )
                        for k in range(len(path) - 1)
                    ]
                    steps[sid] = Step(
                        sid=sid,
                        rule_id="R-EQ-TRANSITIVE",
                        rule_version=rule("R-EQ-TRANSITIVE").version,
                        version=STEP_VERSION,
                        a=x,
                        b=y,
                        chain=chain,
                    )
                pair_step[pair] = sid

    eq_lookup = {f.ordered: f.fid for f in eq_facts}

    conflicts: list[Conflict] = []
    for fact in sorted(neq_facts, key=lambda f: (f.ordered, f.fid)):
        x, y = fact.ordered
        if x == y:
            conflicts.append(_reflexive_conflict(fact))
            continue
        if representatives.get(x) != representatives.get(y):
            continue  # NEQ spanning distinct components is consistent
        rep = representatives[x]
        tree = _bfs_tree(rep, adj)
        chain_path = _path_between(_root_path(tree, x), _root_path(tree, y))
        direct = (x, y) in eq_lookup
        if direct:
            kind, rule_id = "direct_contradiction", "R-EQ-NEQ-DIRECT"
            witness = None
        else:
            kind, rule_id = "inequality_cycle", "R-NEQ-CYCLE"
            witness = pair_step.get((x, y))
        chain_edges = [
            CycleEdge(
                fact_id=edge_fact[tuple(sorted(
                    (chain_path[k], chain_path[k + 1])))],
                a=chain_path[k],
                b=chain_path[k + 1],
                kind=FactKind.EQ,
                step_id=witness,
            )
            for k in range(len(chain_path) - 1)
        ]
        chain_edges.append(CycleEdge(fact_id=fact.fid, a=y, b=x,
                                     kind=FactKind.NEQ, step_id=None))
        basis = "|".join(e.fact_id for e in chain_edges)
        conflicts.append(Conflict(
            cid=digest(f"conflict|{kind}|{basis}"),
            kind=kind,
            neq_fact_id=fact.fid,
            cycle=chain_edges,
            rule_id=rule_id,
            rule_version=rule(rule_id).version,
            version=CONFLICT_VERSION,
        ))
    conflicts.sort(key=lambda c: c.cid)

    entities: list[Entity] = []
    for component in components:
        names: list[str] = []
        for member in component:
            name = labels.get(member)
            if name and name not in names:
                names.append(name)
        entities.append(Entity(representative=component[0],
                               members=component, names=names))

    return DerivedState(
        version=state_version,
        representatives=dict(sorted(representatives.items())),
        entities=entities,
        steps=[steps[k] for k in sorted(steps)],
        conflicts=conflicts,
    )


def _reflexive_conflict(fact: Fact) -> Conflict:
    cycle = [CycleEdge(fact_id=fact.fid, a=fact.a, b=fact.b,
                       kind=FactKind.NEQ, step_id=None)]
    return Conflict(
        cid=digest("conflict|reflexive|" + fact.fid),
        kind="reflexivity",
        neq_fact_id=fact.fid,
        cycle=cycle,
        rule_id="R-NEQ-REFLEXIVE",
        rule_version=rule("R-NEQ-REFLEXIVE").version,
        version=CONFLICT_VERSION,
    )
