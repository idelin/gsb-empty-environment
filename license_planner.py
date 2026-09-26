"""Dependency license planner.

Given a set of dependencies, each constrained by a license expression built
from AND / OR / parentheses over leaf licenses, choose a single set of
licenses that satisfies every dependency simultaneously at minimum total
compliance cost.  Each license is paid for at most once even when several
dependencies use it.  Declared incompatible license pairs may not coexist
in the chosen set.

The solver is exact: it enumerates the minimal satisfying license sets of
every expression and searches their cross product for the cheapest
compatible union, so the reported cost is provably optimal (at exponential
worst-case cost in the size of the input).

Input format (JSON):
{
  "licenses":     {"X": {"cost": 1}, "Y": {"cost": 2}},
  "incompatible": [["X", "Y"]],
  "dependencies": {"A": "X OR Y", "B": "Y"}
}

Output format (JSON):
{
  "status": "optimal" | "infeasible",
  "total_cost": <int>,                # when optimal
  "selected": ["Y"],                  # when optimal
  "witness": {"A": <tree>, ...},      # per-dependency witness path
  "certificate": {...}                # when infeasible
}

Usage: python3 license_planner.py input.json
"""

from __future__ import annotations

import itertools
import json
import sys
from dataclasses import dataclass


# ---------------------------------------------------------------------------
# Expression parsing
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Leaf:
    name: str


@dataclass(frozen=True)
class And:
    left: object
    right: object


@dataclass(frozen=True)
class Or:
    left: object
    right: object


class ParseError(ValueError):
    pass


def tokenize(text):
    tokens = []
    i = 0
    while i < len(text):
        ch = text[i]
        if ch.isspace():
            i += 1
        elif ch in "()":
            tokens.append(ch)
            i += 1
        elif ch.isalpha() or ch == "_":
            j = i
            while j < len(text) and (text[j].isalnum() or text[j] in "_-."):
                j += 1
            word = text[i:j]
            tokens.append(word.upper() if word.upper() in ("AND", "OR") else word)
            i = j
        else:
            raise ParseError(f"unexpected character {ch!r} at position {i}")
    return tokens


def parse_expression(text):
    """Parse an AND/OR/parenthesis expression.  AND binds tighter than OR."""
    tokens = tokenize(text)
    pos = 0

    def peek():
        return tokens[pos] if pos < len(tokens) else None

    def parse_or():
        nonlocal pos
        node = parse_and()
        while peek() == "OR":
            pos += 1
            node = Or(node, parse_and())
        return node

    def parse_and():
        nonlocal pos
        node = parse_atom()
        while peek() == "AND":
            pos += 1
            node = And(node, parse_atom())
        return node

    def parse_atom():
        nonlocal pos
        tok = peek()
        if tok is None:
            raise ParseError("unexpected end of expression")
        if tok == "(":
            pos += 1
            node = parse_or()
            if peek() != ")":
                raise ParseError("missing closing parenthesis")
            pos += 1
            return node
        if tok in ("AND", "OR", ")"):
            raise ParseError(f"unexpected token {tok!r}")
        pos += 1
        return Leaf(tok)

    tree = parse_or()
    if pos != len(tokens):
        raise ParseError(f"trailing token {tokens[pos]!r}")
    return tree


# ---------------------------------------------------------------------------
# Minimal satisfying sets ("options") of an expression
# ---------------------------------------------------------------------------

def _prune(sets):
    """Keep only inclusion-minimal sets."""
    out = []
    for s in sets:
        if any(other < s for other in sets):
            continue
        if s not in out:
            out.append(s)
    return out


def options(node):
    """Return the list of inclusion-minimal license sets satisfying `node`."""
    if isinstance(node, Leaf):
        return [frozenset({node.name})]
    if isinstance(node, Or):
        return _prune(options(node.left) + options(node.right))
    if isinstance(node, And):
        combined = [l | r for l in options(node.left) for r in options(node.right)]
        return _prune(combined)
    raise TypeError(node)


# ---------------------------------------------------------------------------
# Witness path
# ---------------------------------------------------------------------------

def witness(node, selected):
    """Build a witness tree showing how `selected` satisfies `node`.

    Returns None when the selected set does not satisfy the node.
    """
    if isinstance(node, Leaf):
        if node.name in selected:
            return {"license": node.name, "selected": True}
        return None
    if isinstance(node, And):
        left = witness(node.left, selected)
        right = witness(node.right, selected)
        if left is None or right is None:
            return None
        return {"and": [left, right]}
    if isinstance(node, Or):
        for child in (node.left, node.right):
            sub = witness(child, selected)
            if sub is not None:
                return {"or": sub}
        return None
    raise TypeError(node)


# ---------------------------------------------------------------------------
# Solver
# ---------------------------------------------------------------------------

def solve(licenses, incompatible, dependencies):
    """Solve one planning problem.

    licenses:     {name: cost(int)}
    incompatible: iterable of (name, name) pairs
    dependencies: {dep_name: expression string}

    Returns a result dict (see module docstring).
    """
    parsed = {dep: parse_expression(expr) for dep, expr in dependencies.items()}
    for dep, tree in parsed.items():
        for leaf in _leaves(tree):
            if leaf not in licenses:
                raise ValueError(
                    f"dependency {dep!r} references unknown license {leaf!r}")

    pairs = set()
    for a, b in incompatible:
        for name in (a, b):
            if name not in licenses:
                raise ValueError(
                    f"incompatible pair references unknown license {name!r}")
        pairs.add(frozenset((a, b)))

    def conflicts(license_set):
        return sorted(tuple(sorted(p)) for p in pairs if p <= license_set)

    dep_options = {dep: options(tree) for dep, tree in parsed.items()}

    best = None  # (cost, frozenset)
    attempts = []  # per-combination conflicts, for the certificate
    names = sorted(dep_options)
    choices = [dep_options[n] for n in names]

    if all(choices):
        for combo in itertools.product(*choices):
            union = frozenset().union(*combo) if combo else frozenset()
            bad = conflicts(union)
            if bad:
                attempts.append({
                    "combination": {n: sorted(c) for n, c in zip(names, combo)},
                    "conflicts": [list(p) for p in bad],
                })
            else:
                cost = sum(licenses[name] for name in union)
                if best is None or cost < best[0] or (
                        cost == best[0] and sorted(union) < sorted(best[1])):
                    best = (cost, union)

    if best is not None:
        cost, selected = best
        return {
            "status": "optimal",
            "total_cost": cost,
            "selected": sorted(selected),
            "witness": {
                dep: witness(parsed[dep], selected) for dep in sorted(parsed)
            },
        }

    # Infeasible: build a certificate.
    empty = [dep for dep, opts in dep_options.items() if not opts]
    certificate = {
        "reason": "no combination of per-dependency license sets is compatible",
        "options_per_dependency": {
            dep: [sorted(o) for o in opts]
            for dep, opts in sorted(dep_options.items())
        },
        "incompatible_pairs": [sorted(p) for p in sorted(pairs, key=sorted)],
    }
    if empty:
        certificate["unsatisfiable_alone"] = sorted(empty)
    certificate["blocking_combinations"] = attempts
    return {"status": "infeasible", "certificate": certificate}


def _leaves(node):
    if isinstance(node, Leaf):
        yield node.name
    else:
        yield from _leaves(node.left)
        yield from _leaves(node.right)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def load_problem(path):
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    licenses = {name: int(spec["cost"]) for name, spec in data["licenses"].items()}
    incompatible = [tuple(pair) for pair in data.get("incompatible", [])]
    dependencies = data["dependencies"]
    return licenses, incompatible, dependencies


def main(argv):
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    licenses, incompatible, dependencies = load_problem(argv[1])
    result = solve(licenses, incompatible, dependencies)
    json.dump(result, sys.stdout, indent=2, ensure_ascii=False)
    print()
    return 0 if result["status"] == "optimal" else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
