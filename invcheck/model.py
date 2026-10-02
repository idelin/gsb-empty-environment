"""Finite-state model loading and concrete (per-state) semantics.

A model is a JSON object::

    {
      "name": "example",
      "variables": ["a", "b"],
      "init": "!a & !b",
      "transitions": [
        {"name": "tick", "guard": "!a", "assign": {"a": "true"}}
      ]
    }

Unassigned variables keep their value (frame condition).  Several
transitions may be enabled in a state (nondeterminism); a state with no
enabled transition is a deadlock.

The concrete semantics (:func:`next_states`) is used only by the
reference enumerator and by certificate replay -- never by the
symbolic checker itself.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

from . import expr as exprmod


@dataclass
class Transition:
    name: str
    guard: tuple
    assign: dict  # var -> AST


@dataclass
class Model:
    name: str
    variables: list
    init: tuple
    transitions: list
    source: dict


def _as_ast(node, what):
    if isinstance(node, bool):
        return ("const", node)
    if isinstance(node, str):
        return exprmod.parse(node)
    if isinstance(node, tuple) and node and isinstance(node[0], str):
        return node  # already an AST (programmatic use)
    raise ValueError(f"{what} must be a string expression or boolean, got {node!r}")


def parse_model(data):
    if not isinstance(data, dict):
        raise ValueError("model must be a JSON object")
    variables = data.get("variables")
    if not variables or not all(isinstance(v, str) for v in variables):
        raise ValueError("model requires a non-empty 'variables' list of names")
    if len(set(variables)) != len(variables):
        raise ValueError("duplicate variable names")
    init = _as_ast(data.get("init", "false"), "init")
    transitions = []
    for i, raw in enumerate(data.get("transitions", [])):
        if not isinstance(raw, dict):
            raise ValueError(f"transition {i} must be an object")
        guard = _as_ast(raw.get("guard", "true"), f"transition {i} guard")
        assign = {}
        for v, e in (raw.get("assign") or {}).items():
            if v not in variables:
                raise ValueError(f"transition {i} assigns unknown variable {v!r}")
            assign[v] = _as_ast(e, f"transition {i} assignment of {v!r}")
        transitions.append(Transition(raw.get("name", f"t{i}"), guard, assign))
    model = Model(data.get("name", "model"), list(variables), init, transitions, data)
    allowed = set(variables)
    labelled = [("init", init)]
    for t in transitions:
        labelled.append((f"guard of {t.name}", t.guard))
        labelled.extend((f"assignment of {v} in {t.name}", e) for v, e in t.assign.items())
    for label, ast in labelled:
        unknown = exprmod.ast_vars(ast) - allowed
        if unknown:
            raise ValueError(f"{label} references unknown variables: {sorted(unknown)}")
    return model


def load_model(path):
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    return parse_model(data)


def next_states(model, state):
    """Concrete successors of ``state`` (list, one per enabled transition)."""
    out = []
    for t in model.transitions:
        if exprmod.eval_ast(t.guard, state):
            ns = dict(state)
            for v, e in t.assign.items():
                ns[v] = exprmod.eval_ast(e, state)
            out.append(ns)
    return out
