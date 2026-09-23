from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True, slots=True)
class Edge:
    source: str
    input: str
    output: str
    target: str

    @property
    def id(self) -> str:
        return f"{self.source}:{self.input}"


@dataclass(frozen=True, slots=True)
class Machine:
    name: str
    states: tuple[str, ...]
    inputs: tuple[str, ...]
    outputs: tuple[str, ...]
    initial_candidates: tuple[str, ...]
    resettable: frozenset[str]
    input_costs: dict[str, int]
    reset_cost: int
    transitions: dict[tuple[str, str], Edge]
    outgoing: dict[str, tuple[Edge, ...]]
    required_edges: frozenset[str]
    edges_by_id: dict[str, Edge]


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def load_machine(path: str | Path) -> Machine:
    with Path(path).open("r", encoding="utf-8") as handle:
        return machine_from_dict(json.load(handle))


def machine_from_dict(data: dict[str, Any]) -> Machine:
    name = str(data.get("name", "machine"))
    states = tuple(data.get("states", ()))
    inputs = tuple(data.get("inputs", ()))
    outputs = tuple(data.get("outputs", ()))
    initial = tuple(data.get("initial_candidates", ()))
    resettable = frozenset(data.get("resettable", ()))

    _require(len(states) > 0, "states must be non-empty")
    _require(len(inputs) > 0, "inputs must be non-empty")
    _require(len(outputs) > 0, "outputs must be non-empty")
    _require(len(set(states)) == len(states), "duplicate state")
    _require(len(set(inputs)) == len(inputs), "duplicate input")
    _require(len(set(outputs)) == len(outputs), "duplicate output")
    _require(len(initial) > 0, "initial_candidates must be non-empty")
    _require(len(set(initial)) == len(initial), "duplicate initial candidate")
    _require(set(initial) <= set(states), "unknown initial candidate")
    _require(resettable <= set(states), "unknown resettable state")

    raw_costs = data.get("input_costs", {})
    _require(isinstance(raw_costs, dict), "input_costs must be an object")
    _require(set(raw_costs) == set(inputs), "each input needs exactly one positive integer cost")
    input_costs: dict[str, int] = {}
    for symbol in inputs:
        cost = raw_costs[symbol]
        _require(isinstance(cost, int) and not isinstance(cost, bool) and cost > 0,
                 f"cost of input {symbol!r} must be a positive integer")
        input_costs[symbol] = cost

    reset_cost = data.get("reset_cost")
    _require(isinstance(reset_cost, int) and not isinstance(reset_cost, bool) and reset_cost > 0,
             "reset_cost must be a positive integer")

    state_set = set(states)
    input_set = set(inputs)
    output_set = set(outputs)
    transitions: dict[tuple[str, str], Edge] = {}
    edges_by_id: dict[str, Edge] = {}

    for index, raw in enumerate(data.get("transitions", ())):
        _require(isinstance(raw, dict), f"transition {index} must be an object")
        source = raw.get("source")
        symbol = raw.get("input")
        target = raw.get("target")
        output = raw.get("output")
        _require(source in state_set, f"transition {index}: unknown source")
        _require(symbol in input_set, f"transition {index}: unknown input")
        _require(target in state_set, f"transition {index}: unknown target")
        _require(output in output_set, f"transition {index}: unknown output")
        key = (source, symbol)
        _require(key not in transitions, f"duplicate transition for {source!r}, {symbol!r}")
        edge = Edge(source=source, input=symbol, output=output, target=target)
        transitions[key] = edge
        _require(edge.id not in edges_by_id, f"duplicate edge id {edge.id}")
        edges_by_id[edge.id] = edge

    outgoing: dict[str, list[Edge]] = {state: [] for state in states}
    for edge in transitions.values():
        outgoing[edge.source].append(edge)

    reachable_states = set(initial)
    required: set[str] = set()
    stack = list(initial)
    while stack:
        state = stack.pop()
        for edge in outgoing[state]:
            required.add(edge.id)
            if edge.target not in reachable_states:
                reachable_states.add(edge.target)
                stack.append(edge.target)

    return Machine(
        name=name,
        states=states,
        inputs=inputs,
        outputs=outputs,
        initial_candidates=initial,
        resettable=resettable,
        input_costs=input_costs,
        reset_cost=reset_cost,
        transitions=transitions,
        outgoing={state: tuple(sorted(edges, key=lambda edge: edge.input)) for state, edges in outgoing.items()},
        required_edges=frozenset(required),
        edges_by_id=edges_by_id,
    )


@dataclass(frozen=True, slots=True)
class World:
    origin: str
    state: str
    residual: frozenset[str]


@dataclass(frozen=True, slots=True)
class Belief:
    common: frozenset[str]
    worlds: tuple[World, ...]


def initial_belief(machine: Machine) -> Belief:
    worlds = tuple(
        World(origin=state, state=state, residual=frozenset())
        for state in machine.initial_candidates
    )
    return Belief(common=frozenset(), worlds=tuple(sorted(worlds, key=world_key)))


def is_goal(machine: Machine, belief: Belief) -> bool:
    return machine.required_edges <= belief.common


def legal_inputs(machine: Machine, belief: Belief) -> tuple[str, ...]:
    return tuple(
        symbol
        for symbol in machine.inputs
        if all((world.state, symbol) in machine.transitions for world in belief.worlds)
    )


def can_reset(machine: Machine, belief: Belief) -> bool:
    return all(world.state in machine.resettable for world in belief.worlds)


def apply_input(machine: Machine, belief: Belief, symbol: str) -> dict[str, Belief]:
    groups: dict[str, list[tuple[World, Edge]]] = {}
    for world in belief.worlds:
        edge = machine.transitions.get((world.state, symbol))
        if edge is None:
            raise ValueError(f"input {symbol!r} is not safe at {world.state!r}")
        groups.setdefault(edge.output, []).append((world, edge))

    result: dict[str, Belief] = {}
    for output, members in groups.items():
        full_sets = [
            belief.common | world.residual | ({edge.id} if edge.id in machine.required_edges else set())
            for world, edge in members
        ]
        new_common = frozenset(set.intersection(*(set(full) for full in full_sets)))
        next_worlds: dict[tuple[str, str, frozenset[str]], World] = {}
        for (world, edge), full in zip(members, full_sets):
            residual = frozenset(full - new_common)
            key = (world.origin, edge.target, residual)
            next_worlds[key] = World(origin=world.origin, state=edge.target, residual=residual)
        result[output] = Belief(
            common=new_common,
            worlds=tuple(sorted(next_worlds.values(), key=world_key)),
        )
    return result


def apply_reset(machine: Machine, belief: Belief) -> Belief | None:
    if not can_reset(machine, belief):
        return None
    next_worlds: dict[tuple[str, str, frozenset[str]], World] = {}
    for world in belief.worlds:
        key = (world.origin, world.origin, world.residual)
        next_worlds[key] = World(origin=world.origin, state=world.origin, residual=world.residual)
    return Belief(common=belief.common, worlds=tuple(sorted(next_worlds.values(), key=world_key)))


def world_key(world: World) -> tuple[str, str, tuple[str, ...]]:
    return world.origin, world.state, tuple(sorted(world.residual))


def belief_key(belief: Belief) -> str:
    return json.dumps(
        [
            sorted(belief.common),
            [list(world_key(world)) for world in belief.worlds],
        ],
        separators=(",", ":"),
        sort_keys=True,
    )


def belief_audit(belief: Belief) -> dict[str, Any]:
    return {
        "common_covered": sorted(belief.common),
        "worlds": [
            {
                "origin": world.origin,
                "current": world.state,
                "privately_covered": sorted(world.residual),
            }
            for world in belief.worlds
        ],
    }
