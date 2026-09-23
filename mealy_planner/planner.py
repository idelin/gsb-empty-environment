from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from typing import Any

from .model import (
    Belief,
    Machine,
    World,
    apply_input,
    apply_reset,
    belief_audit,
    belief_key,
    can_reset,
    initial_belief,
    is_goal,
    legal_inputs,
)

INF = float("inf")


@dataclass(slots=True)
class ActionNode:
    kind: str
    label: str
    cost: int
    children: dict[str, str]
    upper: float = INF
    policy: dict[str, str] | None = None


@dataclass(slots=True)
class SearchNode:
    belief: Belief
    goal: bool
    expanded: bool = False
    permanently_dead: bool = False
    actions: list[ActionNode] = field(default_factory=list)
    parents: list[tuple[str, ActionNode]] = field(default_factory=list)
    lower: float = 0
    upper: float = INF
    policy_action: ActionNode | None = None
    dead_reason: dict[str, Any] | None = None


class Planner:
    def __init__(self, machine: Machine, budget: int = 100_000):
        self.machine = machine
        self.budget = budget
        self.nodes: dict[str, SearchNode] = {}
        self.queue: list[str] = []
        self.nodes_discovered = 0
        self.nodes_expanded = 0
        self.action_count = 0
        self.truncated = False
        self._heuristic_cache: dict[tuple[str, str, tuple[str, ...]], float] = {}
        self._root = self._make_node(initial_belief(machine))

    def solve(self) -> dict[str, Any]:
        while self.queue and self.nodes_expanded < self.budget:
            key = self.queue.pop(0)
            node = self.nodes[key]
            if node.expanded or node.permanently_dead or node.goal:
                continue
            self._expand(key, node)

        self.truncated = bool(self.queue) and self.nodes_expanded >= self.budget
        self._propagate_upper_bounds()
        if not self.truncated:
            for node in self.nodes.values():
                if node.upper < INF:
                    node.lower = node.upper
                else:
                    node.lower = INF
        root = self.nodes[self._root]

        if root.upper < INF:
            policy = self._build_policy()
            if root.lower == root.upper:
                status = "solved"
            else:
                status = "undecided"
            result: dict[str, Any] = {
                "status": status,
                "optimal_worst_case_cost": int(root.upper) if status == "solved" else None,
                "lower_bound": int(root.lower) if root.lower < INF else None,
                "upper_bound": int(root.upper),
                "strategy": policy,
            }
        elif not self.truncated:
            result = {
                "status": "unsat",
                "optimal_worst_case_cost": None,
                "lower_bound": None,
                "upper_bound": None,
                "strategy": None,
                "diagnosis": self._diagnose(),
            }
        else:
            result = {
                "status": "undecided",
                "optimal_worst_case_cost": None,
                "lower_bound": int(root.lower),
                "upper_bound": None,
                "strategy": None,
            }

        result["search"] = {
            "budget": self.budget,
            "nodes_discovered": self.nodes_discovered,
            "nodes_expanded": self.nodes_expanded,
            "actions_generated": self.action_count,
            "truncated": self.truncated,
            "required_transitions": sorted(self.machine.required_edges),
        }
        result["semantics"] = {
            "reset": "returns each possible trajectory to its true initial candidate; coverage is retained",
            "definite_coverage": "common intersection of all possible actual trajectories in an output branch",
            "objective": "minimum worst-branch positive action cost",
        }
        return result

    def _make_node(self, belief: Belief) -> str:
        key = belief_key(belief)
        if key in self.nodes:
            return key
        node = SearchNode(belief=belief, goal=is_goal(self.machine, belief))
        if node.goal:
            node.expanded = True
            node.upper = 0
        else:
            lower, reason = self._admissible_lower(belief)
            node.lower = lower
            node.dead_reason = reason
            if lower == INF:
                node.permanently_dead = True
            else:
                self.queue.append(key)
        self.nodes[key] = node
        self.nodes_discovered += 1
        return key

    def _expand(self, key: str, node: SearchNode) -> None:
        belief = node.belief
        action_specs: list[tuple[str, str, int, dict[str, Belief]]] = []
        for symbol in legal_inputs(self.machine, belief):
            branches = apply_input(self.machine, belief, symbol)
            action_specs.append(("input", symbol, self.machine.input_costs[symbol], branches))
        reset = apply_reset(self.machine, belief)
        if reset is not None:
            action_specs.append(("reset", "reset", self.machine.reset_cost, {"reset": reset}))

        for kind, label, cost, branches in action_specs:
            child_ids = {output: self._make_node(next_belief) for output, next_belief in branches.items()}
            if len(child_ids) == 1 and next(iter(child_ids.values())) == key:
                continue
            action = ActionNode(kind=kind, label=label, cost=cost, children=child_ids)
            node.actions.append(action)
            self.action_count += 1
            for child_id in child_ids.values():
                self.nodes[child_id].parents.append((key, action))

        node.expanded = True
        self.nodes_expanded += 1

    def _admissible_lower(self, belief: Belief) -> tuple[float, dict[str, Any] | None]:
        values = []
        reasons = []
        for world in belief.worlds:
            missing = self.machine.required_edges - belief.common - world.residual
            distance, reason = self._single_world_distance(world, missing)
            if distance == INF:
                reasons.append(reason)
            values.append(distance)
        if any(value == INF for value in values):
            return INF, {"impossible_worlds": reasons}
        return max(values), None

    def _single_world_distance(
        self,
        world: World,
        missing: frozenset[str],
    ) -> tuple[float, dict[str, Any] | None]:
        cache_key = (world.origin, world.state, tuple(sorted(missing)))
        if cache_key in self._heuristic_cache:
            return self._heuristic_cache[cache_key], None

        start = (world.state, missing)
        distances: dict[tuple[str, frozenset[str]], int] = {start: 0}
        heap: list[tuple[int, str, frozenset[str]]] = [(0, world.state, missing)]
        answer: float = INF
        while heap:
            distance, state, remaining = heapq.heappop(heap)
            current = (state, remaining)
            if distance != distances[current]:
                continue
            if not remaining:
                answer = distance
                break
            for edge in self.machine.outgoing[state]:
                next_remaining = remaining - {edge.id}
                nxt = (edge.target, next_remaining)
                next_distance = distance + self.machine.input_costs[edge.input]
                if next_distance < distances.get(nxt, 10**30):
                    distances[nxt] = next_distance
                    heapq.heappush(heap, (next_distance, edge.target, next_remaining))
            if state in self.machine.resettable:
                nxt = (world.origin, remaining)
                next_distance = distance + self.machine.reset_cost
                if next_distance < distances.get(nxt, 10**30):
                    distances[nxt] = next_distance
                    heapq.heappush(heap, (next_distance, world.origin, remaining))

        self._heuristic_cache[cache_key] = answer
        if answer == INF:
            return INF, self._individual_dead_reason(world, missing)
        return answer, None

    def _individual_dead_reason(self, world: World, missing: frozenset[str]) -> dict[str, Any]:
        reachable = {world.state}
        stack = [world.state]
        while stack:
            state = stack.pop()
            for edge in self.machine.outgoing[state]:
                if edge.target not in reachable:
                    reachable.add(edge.target)
                    stack.append(edge.target)
            if state in self.machine.resettable and world.origin not in reachable:
                reachable.add(world.origin)
                stack.append(world.origin)
        structurally_missing = [
            edge_id for edge_id in sorted(missing)
            if self.machine.edges_by_id[edge_id].source not in reachable
        ]
        return {
            "origin": world.origin,
            "current": world.state,
            "structurally_unreachable_required_transitions": structurally_missing,
            "note": "No finite single-trajectory walk covers all residual required transitions, ignoring observability and safety.",
        }

    def _propagate_upper_bounds(self) -> None:
        heap: list[tuple[int, str]] = []
        for key, node in self.nodes.items():
            if node.goal:
                heapq.heappush(heap, (0, key))
        while heap:
            value, key = heapq.heappop(heap)
            node = self.nodes[key]
            if value != node.upper:
                continue
            for parent_key, action in node.parents:
                child_values = [self.nodes[child].upper for child in action.children.values()]
                if any(child == INF for child in child_values):
                    continue
                candidate = action.cost + max(child_values)
                if candidate < action.upper:
                    action.upper = candidate
                    action.policy = dict(action.children)
                parent = self.nodes[parent_key]
                best = min(parent.actions, key=lambda item: (item.upper, item.kind, item.label))
                if best.upper < parent.upper:
                    parent.upper = best.upper
                    parent.policy_action = best
                    heapq.heappush(heap, (int(best.upper), parent_key))

    def _build_policy(self) -> dict[str, Any]:
        root_action = self.nodes[self._root].policy_action
        pending: list[tuple[str, ActionNode | None]] = [(self._root, root_action)]
        output_nodes: list[dict[str, Any]] = []

        while pending:
            internal_key, chosen = pending.pop(0)
            internal = self.nodes[internal_key]
            node_id = f"n{len(output_nodes)}"
            data: dict[str, Any] = {"id": node_id, "belief_audit": belief_audit(internal.belief)}
            if internal.goal or chosen is None:
                data["type"] = "terminal"
            else:
                data["action_cost"] = chosen.cost
                if chosen.kind == "input":
                    branches = []
                    for output in sorted(chosen.children):
                        child_key = chosen.children[output]
                        child_node = self.nodes[child_key]
                        child_id = f"n{len(output_nodes) + len(pending) + 1}"
                        branches.append({"output": output, "next": child_id})
                        pending.append((child_key, child_node.policy_action))
                    data["type"] = "input"
                    data["input"] = chosen.label
                    data["branches"] = branches
                else:
                    child_key = next(iter(chosen.children.values()))
                    child_node = self.nodes[child_key]
                    child_id = f"n{len(output_nodes) + len(pending) + 1}"
                    data["type"] = "reset"
                    data["next"] = child_id
                    pending.append((child_key, child_node.policy_action))
            output_nodes.append(data)

        return {
            "schema_version": 1,
            "root": "n0",
            "nodes": output_nodes,
        }

    def _diagnose(self) -> dict[str, Any]:
        root = self.nodes[self._root]
        unsafe_inputs = []
        for symbol in self.machine.inputs:
            blockers = sorted({
                world.state for world in root.belief.worlds
                if (world.state, symbol) not in self.machine.transitions
            })
            if blockers:
                unsafe_inputs.append({"input": symbol, "undefined_for_states": blockers})

        reset_blockers = sorted({
            world.state for world in root.belief.worlds
            if world.state not in self.machine.resettable
        })
        impossible = []
        for world in root.belief.worlds:
            missing = self.machine.required_edges - root.belief.common - world.residual
            distance, reason = self._single_world_distance(world, missing)
            if distance == INF:
                impossible.append(reason)

        losing_keys = [key for key, node in self.nodes.items() if node.upper == INF]
        witnesses = self._losing_witnesses(losing_keys)
        return {
            "blocked_belief": belief_audit(root.belief),
            "unsafe_inputs": unsafe_inputs,
            "reset_illegal_for_states": reset_blockers,
            "impossible_worlds": impossible,
            "losing_region_size": len(losing_keys),
            "losing_region_is_closed": self._losing_closure_check(losing_keys),
            "root_action_witnesses": witnesses,
            "explanation": (
                "The root augmented belief cannot reach the common-coverage goal. "
                "Each listed safe action has at least one possible output branch in the closed losing region; "
                "unsafe inputs and illegal resets cannot be executed by a black-box policy."
            ),
        }

    def _losing_closure_check(self, losing: list[str]) -> bool:
        losing_set = set(losing)
        for key in losing:
            node = self.nodes[key]
            if node.goal:
                return False
            if node.permanently_dead:
                continue
            if not node.expanded:
                return False
            for action in node.actions:
                if all(child not in losing_set for child in action.children.values()):
                    return False
        return True

    def _losing_witnesses(self, losing: list[str]) -> list[dict[str, Any]]:
        losing_set = set(losing)
        root = self.nodes[self._root]
        if root.permanently_dead:
            return [{"kind": "permanently_impossible_belief", "reason": root.dead_reason}]
        witnesses = []
        for action in root.actions:
            path = []
            current_key = self._root
            seen: set[str] = set()
            while current_key not in seen:
                seen.add(current_key)
                current = self.nodes[current_key]
                if current.permanently_dead:
                    path.append({"node": belief_audit(current.belief), "kind": "permanently_impossible"})
                    break
                if not current.actions:
                    path.append({"node": belief_audit(current.belief), "kind": "no_safe_action"})
                    break
                chosen = None
                chosen_child = None
                for candidate in current.actions:
                    losing_child = next(
                        (child for child in candidate.children.values() if child in losing_set),
                        None,
                    )
                    if losing_child is not None:
                        chosen = candidate
                        chosen_child = losing_child
                        break
                assert chosen is not None and chosen_child is not None
                chosen_output = next(
                    output for output, child in chosen.children.items() if child == chosen_child
                )
                path.append({
                    "action": {"kind": chosen.kind, "label": chosen.label, "cost": chosen.cost},
                    "output_or_branch": chosen_output,
                    "next_belief": belief_audit(self.nodes[chosen_child].belief),
                })
                current_key = chosen_child
            else:
                path.append({"kind": "closed_cycle_repeat", "node": belief_audit(self.nodes[current_key].belief)})
            witnesses.append({
                "root_action": {"kind": action.kind, "label": action.label, "cost": action.cost},
                "all_branches_losing": all(child in losing_set for child in action.children.values()),
                "one_losing_path": path,
            })
        return witnesses


def plan(machine: Machine, budget: int = 100_000) -> dict[str, Any]:
    if budget < 0:
        raise ValueError("budget must be a non-negative integer")
    return Planner(machine, budget=budget).solve()
