#!/usr/bin/env python3
"""Transactional exact-rational unit conversion graph.

Core ideas:
  * Each conversion rule ``u = k * v`` (k a nonzero rational) is a directed
    edge in a weighted graph.  Edge weights are ``fractions.Fraction`` so no
    floating-point error ever enters the system.
  * Every connected component keeps a "potential" map ``ratio``: for a unit x
    in the component, ``1 x == ratio[x] * (1 root)`` where root is an
    arbitrary representative unit of the component.  Units in different
    components belong to different dimensions.
  * Inserting a rule whose endpoints already live in the same component must
    satisfy ``ratio[u] == k * ratio[v]`` exactly; otherwise the rule creates a
    contradictory cycle and the *entire batch* is rejected, reporting the
    cycle path.
  * Every committed batch is an immutable snapshot (a version).  Queries may
    name a version to replay against historical states.
"""

from __future__ import annotations

import argparse
import json
import sys
from fractions import Fraction


class UnitConversionError(Exception):
    """Base class for all domain errors."""


class UnknownUnitError(UnitConversionError):
    def __init__(self, unit: str):
        self.unit = unit
        super().__init__(f"unknown unit: {unit!r}")


class DimensionMismatchError(UnitConversionError):
    def __init__(self, a: str, b: str):
        self.a, self.b = a, b
        super().__init__(
            f"dimension mismatch: {a!r} and {b!r} are not connected by any "
            "conversion rules"
        )


class InvalidFactorError(UnitConversionError):
    def __init__(self, factor: str):
        self.factor = factor
        super().__init__(f"conversion factor must be a positive rational, got {factor!r}")


class ContradictionError(UnitConversionError):
    """A batch would create a contradictory cycle.

    ``cycle`` is an ordered list of edges ``(u, factor, v)`` meaning
    ``u = factor * v``; following them around returns to the start unit.
    The first edge is the offending new rule, the rest are existing rules
    that already imply a different factor between the same two units.
    """

    def __init__(self, cycle: list[tuple[str, Fraction, str]]):
        self.cycle = cycle
        u, k, v = cycle[0]
        implied = Fraction(1)
        for _, f, _ in cycle[1:]:
            implied *= f
        path_desc = ", ".join(f"{x} ={f} {y}" for x, f, y in cycle[1:])
        super().__init__(
            f"contradiction: new rule {u} ={k} {v} conflicts with existing "
            f"rules ({path_desc}) which imply {u} ={implied} {v}"
        )


def parse_factor(text: str) -> Fraction:
    """Parse a decimal/fraction string into an exact positive Fraction."""
    try:
        value = Fraction(text)
    except (ValueError, ZeroDivisionError) as exc:
        raise InvalidFactorError(text) from exc
    if value <= 0:
        raise InvalidFactorError(text)
    return value


class State:
    """One immutable-ish snapshot of the conversion graph."""

    __slots__ = ("adj", "ratio", "root")

    def __init__(self) -> None:
        # adj[u][v] = f  means  1 u = f * (1 v); reverse edges are stored too.
        self.adj: dict[str, dict[str, Fraction]] = {}
        # ratio[x]: 1 x = ratio[x] * (1 root-of-component)
        self.ratio: dict[str, Fraction] = {}
        # root[x]: representative unit of x's component (its "dimension" id)
        self.root: dict[str, str] = {}

    def clone(self) -> "State":
        new = State()
        new.adj = {u: dict(edges) for u, edges in self.adj.items()}
        new.ratio = dict(self.ratio)
        new.root = dict(self.root)
        return new

    # -- graph maintenance ------------------------------------------------

    def _add_edge(self, u: str, k: Fraction, v: str) -> None:
        self.adj.setdefault(u, {})[v] = k
        self.adj.setdefault(v, {})[u] = Fraction(1, 1) / k

    def _component(self, root: str) -> list[str]:
        return [x for x, r in self.root.items() if r == root]

    def _find_path(self, src: str, dst: str) -> list[tuple[str, Fraction, str]]:
        """BFS path of edges (u, f, v) with u = f*v from src to dst."""
        if src == dst:
            return []
        parent: dict[str, tuple[str, Fraction]] = {}
        queue = [src]
        seen = {src}
        while queue:
            nxt = []
            for u in queue:
                for v, f in self.adj[u].items():
                    if v in seen:
                        continue
                    seen.add(v)
                    parent[v] = (u, f)
                    if v == dst:
                        path: list[tuple[str, Fraction, str]] = []
                        cur = dst
                        while cur != src:
                            p, pf = parent[cur]
                            path.append((p, pf, cur))
                            cur = p
                        path.reverse()
                        return path
                    nxt.append(v)
            queue = nxt
        raise AssertionError("units in same component must be connected")

    def add_rule(self, u: str, k: Fraction, v: str) -> None:
        """Apply one rule to this state; raises ContradictionError on conflict."""
        if u == v:
            if k != 1:
                raise ContradictionError([(u, k, v), (u, Fraction(1), v)])
            self._add_edge(u, k, v)
            self.ratio.setdefault(u, Fraction(1))
            self.root.setdefault(u, u)
            return

        u_known, v_known = u in self.ratio, v in self.ratio

        if u_known and v_known and self.root[u] == self.root[v]:
            if self.ratio[u] != k * self.ratio[v]:
                path = self._find_path(u, v)
                raise ContradictionError([(u, k, v)] + path)
            self._add_edge(u, k, v)
            return

        self._add_edge(u, k, v)
        if u_known and v_known:
            # Merge v's component into u's component.
            # u = k*v  =>  root_v = ratio[u] / (k * ratio[v]) * root_u
            scale = self.ratio[u] / (k * self.ratio[v])
            members = self._component(self.root[v])
            for x in members:
                self.ratio[x] *= scale
                self.root[x] = self.root[u]
        elif u_known:
            self.ratio[v] = self.ratio[u] / k
            self.root[v] = self.root[u]
        elif v_known:
            self.ratio[u] = k * self.ratio[v]
            self.root[u] = self.root[v]
        else:
            # Fresh component rooted at u: 1 u = 1 root, 1 v = 1/k root.
            self.ratio[u] = Fraction(1)
            self.root[u] = u
            self.ratio[v] = Fraction(1, 1) / k
            self.root[v] = u

    # -- queries ------------------------------------------------------------

    def factor_between(self, a: str, b: str) -> Fraction:
        """Return f such that 1 a = f * (1 b)."""
        for unit in (a, b):
            if unit not in self.ratio:
                raise UnknownUnitError(unit)
        if self.root[a] != self.root[b]:
            raise DimensionMismatchError(a, b)
        return self.ratio[a] / self.ratio[b]

    def convert(self, value: Fraction, a: str, b: str) -> Fraction:
        return value * self.factor_between(a, b)


class VersionedConverter:
    """Transactional, versioned conversion service."""

    def __init__(self) -> None:
        self._versions: list[State] = [State()]  # version 0: empty graph

    @property
    def current_version(self) -> int:
        return len(self._versions) - 1

    def _state(self, version: int | None) -> State:
        v = self.current_version if version is None else version
        if not 0 <= v <= self.current_version:
            raise UnitConversionError(
                f"unknown version {v}; valid range is 0..{self.current_version}"
            )
        return self._versions[v]

    def apply_batch(self, rules: list[tuple[str, str, str]]) -> int:
        """Apply a batch of (unit, factor-string, unit) rules atomically.

        Returns the new version number.  On contradiction the whole batch is
        discarded and a ContradictionError carrying the cycle is raised.
        """
        working = self._versions[-1].clone()
        for u, factor_text, v in rules:
            working.add_rule(u, parse_factor(factor_text), v)
        self._versions.append(working)
        return self.current_version

    def convert(
        self, value_text: str, a: str, b: str, version: int | None = None
    ) -> Fraction:
        value = Fraction(value_text)
        return self._state(version).convert(value, a, b)


# --------------------------------------------------------------------------
# CLI: JSON Lines in, JSON Lines out.
# --------------------------------------------------------------------------

def _fmt_cycle(cycle: list[tuple[str, Fraction, str]]) -> list[str]:
    return [f"{u} ={k} {v}" for u, k, v in cycle]


def handle_command(conv: VersionedConverter, cmd: dict) -> dict:
    op = cmd.get("op")
    try:
        if op == "add_batch":
            rules = [(str(u), str(k), str(v)) for u, k, v in cmd["rules"]]
            version = conv.apply_batch(rules)
            return {"ok": True, "version": version}
        if op == "convert":
            result = conv.convert(
                str(cmd["value"]),
                str(cmd["from"]),
                str(cmd["to"]),
                version=cmd.get("version"),
            )
            return {"ok": True, "result": str(result), "version": conv.current_version}
        if op == "factor":
            f = conv._state(cmd.get("version")).factor_between(
                str(cmd["from"]), str(cmd["to"])
            )
            return {"ok": True, "factor": str(f)}
        if op == "versions":
            return {"ok": True, "current_version": conv.current_version}
        return {"ok": False, "error": "bad_request", "detail": f"unknown op {op!r}"}
    except ContradictionError as exc:
        return {
            "ok": False,
            "error": "contradiction",
            "cycle": _fmt_cycle(exc.cycle),
            "detail": str(exc),
        }
    except DimensionMismatchError as exc:
        return {"ok": False, "error": "dimension_mismatch", "detail": str(exc)}
    except UnknownUnitError as exc:
        return {"ok": False, "error": "unknown_unit", "detail": str(exc)}
    except InvalidFactorError as exc:
        return {"ok": False, "error": "invalid_factor", "detail": str(exc)}
    except (UnitConversionError, ValueError, KeyError, TypeError) as exc:
        return {"ok": False, "error": "bad_request", "detail": str(exc)}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Transactional exact-rational unit conversion service "
        "(JSON Lines on stdin/stdout; see README.md)."
    )
    parser.add_argument(
        "--file",
        help="read commands from this file instead of stdin ('-' for stdin)",
    )
    args = parser.parse_args(argv)

    conv = VersionedConverter()
    stream = sys.stdin if args.file in (None, "-") else open(args.file, "r", encoding="utf-8")
    try:
        for line in stream:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            try:
                cmd = json.loads(line)
            except json.JSONDecodeError as exc:
                reply = {"ok": False, "error": "bad_json", "detail": str(exc)}
            else:
                reply = handle_command(conv, cmd)
            print(json.dumps(reply, ensure_ascii=False), flush=True)
    finally:
        if stream is not sys.stdin:
            stream.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
