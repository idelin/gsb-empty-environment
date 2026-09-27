"""Attack/response strategy graphs for simulation failures.

A failed greatest-fixpoint run gives each losing pair ``(t, s)`` a rank (the
deletion round).  At round ``r`` the relation is

    R_r = R_greatest  U  {pairs deleted in round >= r}.

An attack of the pair deleted in round r is a specification move for which
*every* implementation answer lands on a pair that was deleted earlier
(rank strictly smaller), on a state with no answer at all (deadlock / missing
action), or is an unanswerable divergence.  Therefore the strategy graph is
finite and well-founded along response edges: ranks strictly decrease until a
leaf is reached.  It is generally a branching graph, not a single linear
trace; ``extract_trace`` projects one root-to-leaf path for convenience.

The graph is replayable: ``verify_witness`` re-runs the fixpoint, rebuilds the
graph from scratch and compares it edge by edge (endpoint set of every
response included), so a tampered witness cannot pass.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field

from .fixpoint import Budget, FAIL, greatest_simulation
from .lts import LTS, TAU
from .relations import (
    ATTACK_FUNCTIONS,
    RESPONSE_FUNCTIONS,
    Attack,
    Response,
    divergent_states,
)



@dataclass
class PairNode:
    impl_state: str
    spec_state: str
    rank: int
    attacks: list[int] = field(default_factory=list)


@dataclass
class AttackNode:
    attack: Attack
    # each response: (response, target pair node id)
    responses: list[tuple[Response, int]] = field(default_factory=list)


@dataclass
class StrategyGraph:
    mode: str
    divergence_sensitive: bool
    impl_name: str
    spec_name: str
    initial_pair: tuple[str, str]
    final_relation: set[tuple[str, str]]
    rank: dict[tuple[str, str], int]
    rounds: int
    pair_nodes: list[PairNode] = field(default_factory=list)
    attack_nodes: list[AttackNode] = field(default_factory=list)
    root: int = -1

    def to_dict(self) -> dict:
        nodes: list[dict] = []
        pair_index = {id(node): node_id for node_id, node in enumerate(self.pair_nodes)}
        # ids: pair nodes first, attack nodes afterwards; encode via prefix-free ids
        pair_attack: dict[int, int] = {}
        for node_id, node in enumerate(self.pair_nodes):
            nodes.append(
                {
                    "id": f"pair:{node_id}",
                    "type": "pair",
                    "impl": node.impl_state,
                    "spec": node.spec_state,
                    "rank": node.rank,
                    "attacks": [f"attack:{i}" for i in node.attacks],
                }
            )
        for attack_id, node in enumerate(self.attack_nodes):
            nodes.append(
                {
                    "id": f"attack:{attack_id}",
                    "type": "attack",
                    "kind": node.attack.kind,
                    "action": node.attack.action,
                    "spec_next": node.attack.spec_next,
                    "responses": [
                        {
                            **response.to_dict(),
                            "pair": f"pair:{target}",
                        }
                        for response, target in node.responses
                    ],
                }
            )
        return {
            "kind": "simulation-counterexample",
            "version": 1,
            "mode": self.mode,
            "divergence_sensitive": self.divergence_sensitive,
            "impl": self.impl_name,
            "spec": self.spec_name,
            "initial_pair": list(self.initial_pair),
            "rounds": self.rounds,
            "final_relation": [[t, s] for t, s in sorted(self.final_relation)],
            "ranks": [[t, s, r] for (t, s), r in sorted(self.rank.items())],
            "strategy": {"root": f"pair:{self.root}", "nodes": nodes},
        }


def relation_at_round(
    final_relation: set[tuple[str, str]],
    rank: dict[tuple[str, str], int],
    round_index: int,
) -> set[tuple[str, str]]:
    relation = set(final_relation)
    relation.update(pair for pair, deleted_round in rank.items() if deleted_round >= round_index)
    return relation


def build_strategy(
    impl: LTS,
    spec: LTS,
    mode: str,
    divergence_sensitive: bool,
    final_relation: set[tuple[str, str]],
    rank: dict[tuple[str, str], int],
    rounds: int,
) -> StrategyGraph:
    if mode not in ATTACK_FUNCTIONS:
        raise ValueError(f"unknown mode {mode!r}")
    compute_attacks = ATTACK_FUNCTIONS[mode]
    enumerate_responses = RESPONSE_FUNCTIONS[mode]
    impl_divergent = divergent_states(impl) if divergence_sensitive else None
    spec_divergent = divergent_states(spec) if divergence_sensitive else None

    graph = StrategyGraph(
        mode=mode,
        divergence_sensitive=divergence_sensitive,
        impl_name=impl.name,
        spec_name=spec.name,
        initial_pair=(impl.initial, spec.initial),
        final_relation=set(final_relation),
        rank=dict(rank),
        rounds=rounds,
    )

    pair_ids: dict[tuple[str, str], int] = {}
    queue: deque[tuple[str, str]] = deque([(impl.initial, spec.initial)])
    pair_ids[(impl.initial, spec.initial)] = 0
    graph.pair_nodes.append(PairNode(impl.initial, spec.initial, rank[(impl.initial, spec.initial)]))

    while queue:
        impl_state, spec_state = queue.popleft()
        node_id = pair_ids[(impl_state, spec_state)]
        node_rank = rank[(impl_state, spec_state)]
        relation = relation_at_round(final_relation, rank, node_rank)
        attacks = compute_attacks(
            impl,
            spec,
            relation,
            impl_state,
            spec_state,
            impl_divergent,
            spec_divergent,
        )
        if not attacks:
            raise AssertionError(
                f"internal error: losing pair {(impl_state, spec_state)} "
                f"rank {node_rank} has no attack w.r.t. R_{node_rank}"
            )
        for attack in attacks:
            if attack.kind == "divergence":
                responses: list[Response] = []
            elif mode == "weak":
                responses = enumerate_responses(impl, impl_state, attack)
            else:
                responses = enumerate_responses(
                    impl, relation, impl_state, spec_state, attack
                )
            response_edges: list[tuple[Response, int]] = []
            for response in responses:
                target = (response.endpoint, attack.spec_next)
                if target in final_relation:
                    raise AssertionError(
                        f"internal error: response {target} is in the greatest fixpoint"
                    )
                target_rank = rank.get(target)
                if target_rank is None or target_rank >= node_rank:
                    raise AssertionError(
                        f"internal error: response from rank {node_rank} lands on "
                        f"rank {target_rank} ({target}); ranks must strictly decrease"
                    )
                if target not in pair_ids:
                    pair_ids[target] = len(graph.pair_nodes)
                    graph.pair_nodes.append(PairNode(target[0], target[1], target_rank))
                    queue.append(target)
                response_edges.append((response, pair_ids[target]))
            attack_id = len(graph.attack_nodes)
            graph.attack_nodes.append(AttackNode(attack, response_edges))
            graph.pair_nodes[node_id].attacks.append(attack_id)

    graph.root = 0
    return graph


def extract_trace(graph: StrategyGraph) -> dict:
    """Project one root-to-leaf branch of the strategy graph.

    Returns the sequence of specification attacks (visible actions and tau),
    the concrete implementation answer chosen at each step, and the terminal
    reason: 'no-response' (deadlock or no matching move) or 'divergence'.
    """
    steps: list[dict] = []
    current = graph.root
    seen: set[int] = set()
    while current not in seen:
        seen.add(current)
        pair_node = graph.pair_nodes[current]
        if not pair_node.attacks:
            return {"steps": steps, "terminal_reason": "no-attack (internal error)"}
        attack_id = pair_node.attacks[0]
        attack_node = graph.attack_nodes[attack_id]
        attack = attack_node.attack
        step = {
            "pair": [pair_node.impl_state, pair_node.spec_state],
            "pair_rank": pair_node.rank,
            "attack": attack.to_dict(),
            "response": None,
            "response_pair": None,
        }
        if attack.kind == "divergence":
            steps.append(step)
            return {"steps": steps, "terminal_reason": "divergence"}
        if not attack_node.responses:
            steps.append(step)
            return {
                "steps": steps,
                "terminal_reason": "no-response (implementation deadlocks or has no matching move)",
            }
        response, target = attack_node.responses[0]
        step["response"] = response.to_dict()
        target_node = graph.pair_nodes[target]
        step["response_pair"] = [target_node.impl_state, target_node.spec_state]
        steps.append(step)
        current = target
    return {"steps": steps, "terminal_reason": "cycle (internal error: ranks must prevent this)"}


def witness_dict(graph: StrategyGraph) -> dict:
    document = graph.to_dict()
    document["trace"] = extract_trace(graph)
    return document


# ---------------------------------------------------------------------------
# Replay verification
# ---------------------------------------------------------------------------


def _canonical(document: dict) -> str:
    import json

    return json.dumps(document, sort_keys=True, ensure_ascii=False)


def verify_witness(impl: LTS, spec: LTS, witness: dict) -> list[str]:
    """Independently replay a witness document against the two models.

    Returns the list of discrepancies; an empty list means the witness is a
    valid, replayable proof that the initial pair is not simulated.
    """
    errors: list[str] = []
    if witness.get("kind") != "simulation-counterexample":
        errors.append("witness 'kind' is not 'simulation-counterexample'")
        return errors
    if witness.get("version") != 1:
        errors.append(f"unsupported witness version {witness.get('version')!r}")
    mode = witness.get("mode")
    if mode not in ATTACK_FUNCTIONS:
        errors.append(f"witness mode {mode!r} is not 'weak' or 'branching'")
        return errors
    divergence_sensitive = bool(witness.get("divergence_sensitive"))
    if witness.get("impl") != impl.name:
        errors.append(f"impl name mismatch: witness says {witness.get('impl')!r}, model says {impl.name!r}")
    if witness.get("spec") != spec.name:
        errors.append(f"spec name mismatch: witness says {witness.get('spec')!r}, model says {spec.name!r}")
    if witness.get("initial_pair") != [impl.initial, spec.initial]:
        errors.append("initial_pair does not match model initial states")

    result = greatest_simulation(
        impl,
        spec,
        mode=mode,
        divergence_sensitive=divergence_sensitive,
        budget=None,
    )
    if result.status != FAIL:
        errors.append(f"re-run of the fixpoint gives status {result.status!r}, expected 'fail'")
        return errors
    claimed_relation = {tuple(pair) for pair in witness.get("final_relation", [])}
    if claimed_relation != result.relation:
        errors.append("final_relation in witness differs from the recomputed greatest fixpoint")
    claimed_rank = {tuple(pair[:2]): pair[2] for pair in witness.get("ranks", [])}
    if claimed_rank != result.rank:
        errors.append("ranks in witness differ from the recomputed deletion rounds")

    rebuilt = build_strategy(
        impl,
        spec,
        mode,
        divergence_sensitive,
        result.relation,
        result.rank,
        result.rounds,
    )
    rebuilt_document = rebuilt.to_dict()
    claimed_strategy = witness.get("strategy", {})
    if _canonical(claimed_strategy) != _canonical(rebuilt_document["strategy"]):
        errors.append("strategy graph does not match the graph rebuilt from the models")

    # Direct well-formedness checks on the claimed graph, independently of the
    # rebuild, so that even a self-consistent-looking forgery is described.
    nodes = {node["id"]: node for node in claimed_strategy.get("nodes", [])}
    root_id = claimed_strategy.get("root")
    if root_id not in nodes:
        errors.append("strategy root is missing")
    for node_id, node in nodes.items():
        if node.get("type") == "attack":
            for response in node.get("responses", []):
                target_id = response.get("pair")
                target = nodes.get(target_id)
                if target is None:
                    errors.append(f"{node_id}: response points to missing node {target_id!r}")
                    continue
                source_pair = node_id
                source_rank = _rank_of_attack_parent(witness, source_pair)
                if source_rank is not None and target.get("rank", -1) >= source_rank:
                    errors.append(
                        f"{node_id}: response reaches rank {target.get('rank')} "
                        f">= parent rank {source_rank}; ranks must strictly decrease"
                    )
    return errors


def _rank_of_attack_parent(witness: dict, attack_id: str) -> int | None:
    for node in witness.get("strategy", {}).get("nodes", []):
        if node.get("type") == "pair" and attack_id in node.get("attacks", []):
            return node.get("rank")
    return None
