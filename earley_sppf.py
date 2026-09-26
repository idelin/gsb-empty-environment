"""Earley parsing with a shared packed parse forest.

The implementation intentionally uses only Python's standard library.  Grammars
are supplied as explicit terminal alphabets and context-free productions; no
regular-expression or automaton package is used.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
import math
from typing import Optional


@dataclass(frozen=True)
class Production:
    """A grammar transition ``lhs -> rhs``; ``rhs == ()`` means epsilon."""

    lhs: str
    rhs: tuple[str, ...]

    def __str__(self) -> str:
        return f"{self.lhs} → {' '.join(self.rhs) if self.rhs else 'ε'}"


@dataclass(frozen=True)
class Item:
    production_id: int
    dot: int
    start: int


class ForestNode:
    """A terminal, grammar-symbol, or dotted-production SPPF node.

    Identity is object identity.  ``families`` contains packed alternatives:
    an empty tuple represents an epsilon production, while a one- or
    two-element tuple connects a symbol node to a rule node or a dotted rule
    to its previously built prefix and newest child.
    """

    __slots__ = ("id", "kind", "start", "end", "symbol", "production", "dot", "families")

    def __init__(
        self,
        node_id: int,
        kind: str,
        start: int,
        end: int,
        *,
        symbol: Optional[str] = None,
        production: Optional[Production] = None,
        dot: Optional[int] = None,
    ) -> None:
        self.id = node_id
        self.kind = kind
        self.start = start
        self.end = end
        self.symbol = symbol
        self.production = production
        self.dot = dot
        self.families: set[tuple[ForestNode, ...]] = set()

    def __repr__(self) -> str:
        if self.kind == "terminal":
            label = repr(self.symbol)
        elif self.kind == "symbol":
            label = str(self.symbol)
        else:
            assert self.production is not None
            assert self.dot is not None
            rhs = list(self.production.rhs)
            marked = rhs[: self.dot] + ["•"] + rhs[self.dot :]
            if not rhs:
                marked = ["ε", "•"]
            label = f"{self.production.lhs} → {' '.join(marked)}"
        return f"N{self.id} {self.kind}:{label} [{self.start},{self.end}]"


@dataclass(frozen=True)
class ParseResult:
    accepted: bool
    farthest_position: int
    root: Optional[ForestNode]
    nodes: tuple[ForestNode, ...]

    def reachable_nodes(self) -> tuple[ForestNode, ...]:
        """Return nodes reachable from the successful root, if any."""
        if self.root is None:
            return ()
        seen: set[int] = set()
        stack = [self.root]
        while stack:
            node = stack.pop()
            if node.id in seen:
                continue
            seen.add(node.id)
            for children in node.families:
                stack.extend(children)
        return tuple(node for node in self.nodes if node.id in seen)

    def derivation_count(self) -> float:
        """Count derivations using dynamic programming; never enumerate trees.

        A cyclic epsilon grammar can admit infinitely many derivations.  Such a
        cycle is represented by the cyclic SPPF and reported as ``math.inf``.
        """
        if self.root is None:
            return 0

        memo: dict[int, float] = {}
        visiting: set[int] = set()

        def count(node: ForestNode) -> float:
            if node.id in memo:
                return memo[node.id]
            if node.id in visiting:
                return math.inf

            visiting.add(node.id)
            if node.kind == "terminal":
                value = 1
            elif node.kind == "rule" and node.dot == 0:
                # A dotted rule before any child is the empty successful
                # prefix.  For an epsilon rule this node also has the empty
                # packed family.
                value = 1
            else:
                value = 0
                for children in node.families:
                    ways = 1
                    for child in children:
                        ways *= count(child)
                    value += ways
            visiting.remove(node.id)
            memo[node.id] = value
            return value

        return count(self.root)

    def forest_summary(self) -> dict[str, object]:
        nodes = self.reachable_nodes()
        packed_families = sum(len(node.families) for node in nodes)
        ambiguous = [
            node
            for node in nodes
            if node.kind != "terminal" and len(node.families) > 1
        ]
        return {
            "nodes": len(nodes),
            "terminal_nodes": sum(node.kind == "terminal" for node in nodes),
            "symbol_nodes": sum(node.kind == "symbol" for node in nodes),
            "rule_nodes": sum(node.kind == "rule" for node in nodes),
            "packed_families": packed_families,
            "ambiguous_nodes": len(ambiguous),
            "derivations": self.derivation_count(),
        }


class Grammar:
    def __init__(
        self,
        alphabet,
        productions,
        start_symbol: str,
    ) -> None:
        self.alphabet = tuple(alphabet)
        self.terminals = frozenset(self.alphabet)
        self.start_symbol = start_symbol

        normalized: list[Production] = []
        for production in productions:
            if isinstance(production, Production):
                normalized.append(production)
            else:
                lhs, rhs = production
                normalized.append(Production(lhs, tuple(rhs)))
        self.productions = tuple(normalized)

        nonterminals = {production.lhs for production in self.productions}
        if not isinstance(start_symbol, str) or start_symbol not in nonterminals:
            raise ValueError("start_symbol must be a nonterminal with at least one production")
        if start_symbol in self.terminals:
            raise ValueError("start symbol cannot also be a terminal")

        for production in self.productions:
            if not isinstance(production.lhs, str):
                raise ValueError("production left-hand sides must be strings")
            if production.lhs in self.terminals:
                raise ValueError(f"{production.lhs!r} is both a terminal and a nonterminal")
            for symbol in production.rhs:
                if not isinstance(symbol, str):
                    raise ValueError("grammar symbols must be strings")
                if symbol not in self.terminals and symbol not in nonterminals:
                    raise ValueError(f"undefined grammar symbol: {symbol!r}")

        self._by_nonterminal: dict[str, list[int]] = {}
        for production_id, production in enumerate(self.productions):
            self._by_nonterminal.setdefault(production.lhs, []).append(production_id)

    def parse(self, tokens) -> ParseResult:
        tokens = tuple(tokens)
        return _EarleyRunner(self, tokens).run()


class _EarleyRunner:
    def __init__(self, grammar: Grammar, tokens: tuple[str, ...]) -> None:
        self.grammar = grammar
        self.productions = grammar.productions
        self.tokens = tokens

        self.charts: list[dict[Item, ForestNode]] = [{}]
        self.queues: list[deque[Item]] = [deque()]
        self.nodes: list[ForestNode] = []
        self.rule_nodes: dict[tuple[int, int, int, int], ForestNode] = {}
        self.symbol_nodes: dict[tuple[str, int, int], ForestNode] = {}
        self.terminal_nodes: dict[tuple[str, int, int], ForestNode] = {}

    def rule_node(self, production_id: int, dot: int, start: int, end: int) -> ForestNode:
        key = (production_id, dot, start, end)
        node = self.rule_nodes.get(key)
        if node is None:
            node = ForestNode(
                len(self.nodes),
                "rule",
                start,
                end,
                production=self.productions[production_id],
                dot=dot,
            )
            self.nodes.append(node)
            self.rule_nodes[key] = node
        return node

    def symbol_node(self, symbol: str, start: int, end: int) -> ForestNode:
        key = (symbol, start, end)
        node = self.symbol_nodes.get(key)
        if node is None:
            node = ForestNode(len(self.nodes), "symbol", start, end, symbol=symbol)
            self.nodes.append(node)
            self.symbol_nodes[key] = node
        return node

    def terminal_node(self, symbol: str, start: int, end: int) -> ForestNode:
        key = (symbol, start, end)
        node = self.terminal_nodes.get(key)
        if node is None:
            node = ForestNode(len(self.nodes), "terminal", start, end, symbol=symbol)
            self.nodes.append(node)
            self.terminal_nodes[key] = node
        return node

    def ensure_chart(self, position: int) -> None:
        while len(self.charts) <= position:
            self.charts.append({})
            self.queues.append(deque())

    def predict(self, position: int, production_id: int) -> None:
        item = Item(production_id, 0, position)
        node = self.rule_node(production_id, 0, position, position)
        if item not in self.charts[position]:
            self.charts[position][item] = node
            self.queues[position].append(item)

        if not self.productions[production_id].rhs:
            node.families.add(())

    def predict_nonterminal(self, position: int, symbol: str) -> None:
        for production_id in self.grammar._by_nonterminal[symbol]:
            self.predict(position, production_id)

    def advance(
        self,
        end: int,
        waiting_item: Item,
        waiting_node: ForestNode,
        child_node: ForestNode,
    ) -> None:
        production = self.productions[waiting_item.production_id]
        advanced_dot = waiting_item.dot + 1
        advanced_item = Item(waiting_item.production_id, advanced_dot, waiting_item.start)
        advanced_node = self.rule_node(
            waiting_item.production_id,
            advanced_dot,
            waiting_item.start,
            end,
        )

        is_new_state = advanced_item not in self.charts[end]
        if is_new_state:
            self.charts[end][advanced_item] = advanced_node
            self.queues[end].append(advanced_item)

        advanced_node.families.add((waiting_node, child_node))

        if advanced_dot == len(production.rhs):
            completed_symbol = self.symbol_node(production.lhs, waiting_item.start, end)
            completed_symbol.families.add((advanced_node,))

    def run(self) -> ParseResult:
        self.predict_nonterminal(0, self.grammar.start_symbol)
        endpoint = len(self.tokens)
        position = 0

        while position <= endpoint:
            queue = self.queues[position]
            while queue:
                item = queue.popleft()
                node = self.charts[position][item]
                production = self.productions[item.production_id]

                if item.dot == len(production.rhs):
                    completed_symbol = self.symbol_node(production.lhs, item.start, position)
                    completed_symbol.families.add((node,))
                    for waiting_item, waiting_node in list(self.charts[item.start].items()):
                        waiting_production = self.productions[waiting_item.production_id]
                        if (
                            waiting_item.dot < len(waiting_production.rhs)
                            and waiting_production.rhs[waiting_item.dot] == production.lhs
                        ):
                            self.advance(
                                position,
                                waiting_item,
                                waiting_node,
                                completed_symbol,
                            )
                    continue

                next_symbol = production.rhs[item.dot]
                if next_symbol in self.grammar.terminals:
                    if position < endpoint and self.tokens[position] == next_symbol:
                        self.ensure_chart(position + 1)
                        terminal = self.terminal_node(next_symbol, position, position + 1)
                        self.advance(position + 1, item, node, terminal)
                else:
                    self.predict_nonterminal(position, next_symbol)
                    for completed_item, completed_node in list(self.charts[position].items()):
                        completed_production = self.productions[completed_item.production_id]
                        if (
                            completed_item.start == position
                            and completed_item.dot == len(completed_production.rhs)
                            and completed_production.lhs == next_symbol
                        ):
                            completed_symbol = self.symbol_node(next_symbol, position, position)
                            self.advance(position, item, node, completed_symbol)

            if position == endpoint:
                break
            if position + 1 >= len(self.charts) or not self.charts[position + 1]:
                break
            position += 1

        root = self.symbol_nodes.get((self.grammar.start_symbol, 0, endpoint))
        farthest = max(index for index, chart in enumerate(self.charts) if chart)
        return ParseResult(
            accepted=root is not None,
            farthest_position=farthest,
            root=root,
            nodes=tuple(self.nodes),
        )
