from __future__ import annotations

import json
from pathlib import Path
from typing import Any


class VerificationError(Exception):
    pass


def _read(path: str | Path) -> dict[str, Any]:
    with Path(path).open("r", encoding="utf-8") as handle:
        return json.load(handle)


def verify(machine_path: str | Path, policy_path: str | Path) -> dict[str, Any]:
    machine = _read(machine_path)
    envelope = _read(policy_path)
    policy = envelope.get("strategy", envelope)

    states = set(machine["states"])
    required = _reachable_required(machine)
    transitions = {
        (item["source"], item["input"]): item
        for item in machine["transitions"]
    }
    resettable = set(machine["resettable"])
    costs = machine["input_costs"]
    reset_cost = machine["reset_cost"]
    initial = tuple(machine["initial_candidates"])

    nodes = _validate_policy_shape(policy)
    acyclic, cycle = _acyclic(policy["root"], nodes)
    if not acyclic:
        raise VerificationError(f"policy contains a cycle through {cycle!r}")

    root = policy["root"]
    context_id = 0
    pending: list[dict[str, Any]] = [
        {
            "worlds": {state: {"state": state, "cost": 0, "covered": set()} for state in initial},
            "node": root,
            "history": (),
        }
    ]
    reachable_nodes = {root}
    final_costs: dict[str, int] = {}
    final_coverage: dict[str, set[str]] = {}
    warnings: list[str] = []
    terminals = 0

    while pending:
        context = pending.pop()
        node_id = context["node"]
        node = nodes[node_id]
        worlds = context["worlds"]
        history = context["history"]

        if node["type"] == "terminal":
            terminals += 1
            for origin, data in worlds.items():
                missing = required - data["covered"]
                if missing:
                    raise VerificationError(
                        f"terminal reached for origin {origin!r} but missing {sorted(missing)}"
                    )
                previous = final_costs.get(origin)
                if previous is not None and previous != data["cost"]:
                    raise VerificationError(f"origin {origin!r} reached terminals with different costs")
                final_costs[origin] = data["cost"]
                final_coverage[origin] = set(data["covered"])
            continue

        if node["type"] == "input":
            symbol = node["input"]
            undefined = sorted(state for state, data in worlds.items() if (data["state"], symbol) not in transitions)
            if undefined:
                raise VerificationError(f"unsafe input {symbol!r} at node {node_id!r}: undefined for {undefined}")
            groups: dict[str, dict[str, dict[str, Any]]] = {}
            for origin, data in worlds.items():
                edge = transitions[(data["state"], symbol)]
                output = edge["output"]
                covered = set(data["covered"])
                edge_id = f"{edge['source']}:{edge['input']}"
                if edge_id in required:
                    covered.add(edge_id)
                groups.setdefault(output, {})[origin] = {
                    "state": edge["target"],
                    "cost": data["cost"] + costs[symbol],
                    "covered": covered,
                }
            branches = {item["output"]: item["next"] for item in node["branches"]}
            for output, next_worlds in groups.items():
                if output not in branches:
                    raise VerificationError(
                        f"node {node_id!r} has no branch for observed output {output!r}"
                    )
                reachable_nodes.add(branches[output])
                pending.append({
                    "worlds": next_worlds,
                    "node": branches[output],
                    "history": history + ((node_id, "input", symbol, output),),
                })
            for output in sorted(set(branches) - set(groups)):
                warnings.append(f"node {node_id!r} branch {output!r} is unreachable")

        elif node["type"] == "reset":
            blocked = sorted(state for state, data in worlds.items() if data["state"] not in resettable)
            if blocked:
                raise VerificationError(f"illegal reset at node {node_id!r} from states {blocked}")
            next_worlds = {}
            for origin, data in worlds.items():
                next_worlds[origin] = {
                    "state": origin,
                    "cost": data["cost"] + reset_cost,
                    "covered": set(data["covered"]),
                }
            reachable_nodes.add(node["next"])
            pending.append({
                "worlds": next_worlds,
                "node": node["next"],
                "history": history + ((node_id, "reset"),),
            })
        else:
            raise VerificationError(f"unknown node type {node['type']!r}")

    if set(final_costs) != set(initial):
        raise VerificationError("not every initial candidate reached a terminal")
    unreachable = sorted(set(nodes) - reachable_nodes)
    if unreachable:
        warnings.append(f"unreachable policy nodes: {unreachable}")

    worst_cost = max(final_costs.values())
    declared = policy.get("expected_worst_case_cost")
    if declared is not None and declared != worst_cost:
        raise VerificationError(f"declared cost {declared} != verified cost {worst_cost}")

    return {
        "valid": True,
        "worst_case_cost": worst_cost,
        "per_actual_initial_state": {state: final_costs[state] for state in initial},
        "covered_transitions": sorted(required),
        "terminal_contexts": terminals,
        "warnings": warnings,
    }


def _reachable_required(machine: dict[str, Any]) -> set[str]:
    transitions = {
        (item["source"], item["input"]): item
        for item in machine["transitions"]
    }
    outgoing = {state: [] for state in machine["states"]}
    for edge in machine["transitions"]:
        outgoing[edge["source"]].append(edge)
    seen = set(machine["initial_candidates"])
    required = set()
    stack = list(seen)
    while stack:
        state = stack.pop()
        for edge in outgoing[state]:
            required.add(f"{edge['source']}:{edge['input']}")
            if edge["target"] not in seen:
                seen.add(edge["target"])
                stack.append(edge["target"])
    return required


def _validate_policy_shape(policy: dict[str, Any]) -> dict[str, dict[str, Any]]:
    if policy.get("schema_version") != 1:
        raise VerificationError("strategy.schema_version must be 1")
    if "root" not in policy or "nodes" not in policy:
        raise VerificationError("strategy needs root and nodes")
    nodes = {node.get("id"): node for node in policy["nodes"]}
    if len(nodes) != len(policy["nodes"]):
        raise VerificationError("duplicate policy node id")
    if policy["root"] not in nodes:
        raise VerificationError("unknown root node")
    for node_id, node in nodes.items():
        kind = node.get("type")
        if kind == "terminal":
            if "branches" in node or "next" in node:
                raise VerificationError(f"terminal {node_id!r} must not have successors")
        elif kind == "input":
            branches = node.get("branches")
            if not isinstance(branches, list) or not branches:
                raise VerificationError(f"input node {node_id!r} needs non-empty branches")
            outputs = []
            for branch in branches:
                if branch.get("next") not in nodes:
                    raise VerificationError(f"node {node_id!r} points to unknown node")
                outputs.append(branch.get("output"))
            if len(outputs) != len(set(outputs)):
                raise VerificationError(f"duplicate output branch at {node_id!r}")
        elif kind == "reset":
            if node.get("next") not in nodes:
                raise VerificationError(f"reset node {node_id!r} points to unknown node")
        else:
            raise VerificationError(f"node {node_id!r} has invalid type")
    return nodes


def _acyclic(root: str, nodes: dict[str, dict[str, Any]]) -> tuple[bool, str | None]:
    color = {node_id: 0 for node_id in nodes}
    stack: list[tuple[str, int]] = [(root, 0)]
    while stack:
        node_id, edge_index = stack[-1]
        if color[node_id] == 0:
            color[node_id] = 1
        node = nodes[node_id]
        successors = []
        if node["type"] == "input":
            successors = [branch["next"] for branch in node["branches"]]
        elif node["type"] == "reset":
            successors = [node["next"]]
        if edge_index == len(successors):
            color[node_id] = 2
            stack.pop()
            continue
        stack[-1] = (node_id, edge_index + 1)
        nxt = successors[edge_index]
        if color[nxt] == 1:
            return False, nxt
        if color[nxt] == 0:
            stack.append((nxt, 0))
    return True, None
