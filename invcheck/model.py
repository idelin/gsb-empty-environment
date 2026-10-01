"""Finite-state model description and BDD/AST semantics.

A model is a JSON object (or equivalent dict)::

    {
      "name": "mod3",
      "variables": ["b1", "b0"],           # state bits, MSB first
      "init": "~b1 & ~b0",                 # expression over state vars
      "invariant": "~(b1 & b0)",           # candidate invariant
      "transition": { ... }                # one of the three forms below
    }

Transition forms:

* ``{"edges": [[0, 1], [1, 2], ...]}`` -- explicit integer-encoded edges
  (variables listed in ``variables`` are read MSB-first).
* ``{"next": {"b0": "<expr>", ...}}`` -- next-state functions, meaning
  ``b0' <-> <expr>`` for each listed variable; unlisted variables keep
  their value (stutter).
* ``{"relation": "<expr over x and x'>"}`` -- a flat transition relation.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from . import expr as expr_mod


def next_name(var):
    return var + "'"


def is_next_name(var):
    return var.endswith("'")


def strip_prime(var):
    return var[:-1] if var.endswith("'") else var


@dataclass
class Model:
    name: str
    variables: list
    init: str
    invariant: str
    transition: dict
    _init_ast: object = field(default=None, repr=False)
    _inv_ast: object = field(default=None, repr=False)

    def __post_init__(self):
        if not self.variables and self.variables != []:
            raise ValueError("variables must be a list")
        dup = set()
        for v in self.variables:
            if v in dup:
                raise ValueError("duplicate variable %r" % v)
            dup.add(v)
            if is_next_name(v):
                raise ValueError("state variable may not end with a prime: %r" % v)
        self._init_ast = expr_mod.parse(self.init)
        self._inv_ast = expr_mod.parse(self.invariant)
        unknown_init = expr_mod.ast_variables(self._init_ast) - set(self.variables)
        unknown_inv = expr_mod.ast_variables(self._inv_ast) - set(self.variables)
        if unknown_init:
            raise ValueError("init uses unknown variables: %s" % sorted(unknown_init))
        if unknown_inv:
            raise ValueError("invariant uses unknown variables: %s" % sorted(unknown_inv))
        kind = self.transition_kind()
        if kind == "next":
            for v in self.transition["next"]:
                if v not in dup:
                    raise ValueError("next-function for unknown variable %r" % v)
        elif kind == "edges":
            limit = 1 << len(self.variables)
            for edge in self.transition["edges"]:
                if len(edge) != 2 or not all(0 <= s < limit for s in edge):
                    raise ValueError("bad edge %r for %d variables" % (edge, len(self.variables)))
        elif kind == "relation":
            allowed = set(self.variables) | {next_name(v) for v in self.variables}
            unknown = expr_mod.ast_variables(expr_mod.parse(self.transition["relation"])) - allowed
            if unknown:
                raise ValueError("relation uses unknown variables: %s" % sorted(unknown))
        else:
            raise ValueError("transition must have exactly one of: edges, next, relation")

    # ------------------------------------------------------------------ basics

    def transition_kind(self):
        kinds = [k for k in ("edges", "next", "relation") if k in self.transition]
        if len(kinds) != 1:
            return None
        return kinds[0]

    def next_variables(self):
        return [next_name(v) for v in self.variables]

    def all_variables(self):
        """Default variable order: interleaved current/next pairs."""
        out = []
        for v in self.variables:
            out.append(v)
            out.append(next_name(v))
        return out

    def state_value(self, assignment):
        """Integer encoding of a state assignment (MSB-first)."""
        value = 0
        for v in self.variables:
            value = (value << 1) | (1 if assignment.get(v, False) else 0)
        return value

    def value_to_state(self, value):
        n = len(self.variables)
        return {v: bool((value >> (n - 1 - i)) & 1) for i, v in enumerate(self.variables)}

    def canonical_dict(self):
        return {
            "name": self.name,
            "variables": list(self.variables),
            "init": self.init,
            "invariant": self.invariant,
            "transition": self.transition,
        }

    # -------------------------------------------------- AST-level semantics
    # These evaluate the *parsed expressions* directly and never touch the
    # BDD manager, so they serve as an independent semantic path used by
    # the reference checker and by counterexample replay.

    def init_holds(self, state):
        return expr_mod.eval_ast(self._init_ast, state)

    def invariant_holds(self, state, inv_ast=None):
        return expr_mod.eval_ast(inv_ast if inv_ast is not None else self._inv_ast, state)

    def invariant_ast(self):
        return self._inv_ast

    def transition_holds(self, state, next_state):
        kind = self.transition_kind()
        if kind == "edges":
            edges = getattr(self, "_edge_set", None)
            if edges is None:
                edges = {tuple(e) for e in self.transition["edges"]}
                self._edge_set = edges
            return (self.state_value(state), self.state_value(next_state)) in edges
        if kind == "next":
            for v in self.variables:
                f = self.transition["next"].get(v)
                want = expr_mod.eval_ast(expr_mod.parse(f), state) if f is not None else state.get(v, False)
                if bool(next_state.get(v, False)) != want:
                    return False
            return True
        if kind == "relation":
            merged = dict(state)
            for v in self.variables:
                merged[next_name(v)] = bool(next_state.get(v, False))
            return expr_mod.eval_ast(expr_mod.parse(self.transition["relation"]), merged)
        raise ValueError("invalid transition spec")


def load_model(source):
    """Load a Model from a path, JSON string, or dict."""
    if isinstance(source, Model):
        return source
    if isinstance(source, dict):
        data = source
    else:
        text = None
        try:
            with open(source, "r", encoding="utf-8") as fh:
                text = fh.read()
        except (OSError, TypeError):
            text = source
        data = json.loads(text)
    return Model(
        name=data.get("name", "model"),
        variables=list(data["variables"]),
        init=data["init"],
        invariant=data["invariant"],
        transition=data["transition"],
    )
